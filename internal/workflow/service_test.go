package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/usememos/memogram/internal/config"
)

type generatorFunc func(context.Context, Request) (Result, error)

func (f generatorFunc) Generate(ctx context.Context, r Request) (Result, error) { return f(ctx, r) }

type destination struct {
	creates    int
	contents   []string
	uploads    map[string]int
	fail       map[string]bool
	createFail bool
}

func (d *destination) Create(_ context.Context, text string) (Memo, error) {
	if d.createFail {
		return Memo{}, errors.New("unavailable")
	}
	d.creates++
	d.contents = append(d.contents, text)
	return Memo{Name: "memos/one", URL: "https://notes.test/memos/one"}, nil
}
func (d *destination) Upload(_ context.Context, _ Memo, a Attachment) error {
	if d.uploads == nil {
		d.uploads = map[string]int{}
	}
	d.uploads[a.ID]++
	if d.fail[a.ID] {
		return errors.New("upload unavailable")
	}
	return nil
}
func settings() *config.Notebook {
	return &config.Notebook{MaxMaterials: 100, MaxTextBytes: 10000, MaxImages: 8,
		Models:   map[string]config.Model{"a": {Vision: true}, "b": {Vision: true}},
		Profiles: map[string]config.Profile{"tidy": {Model: "a"}, "tutorial": {Model: "a", Images: true}}}
}

const session = "telegram:private:42"

func add(t *testing.T, s *Service, m Material) View {
	t.Helper()
	v, err := s.Add(session, m)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func generate(t *testing.T, s *Service, profile, instruction string) View {
	t.Helper()
	v, work, err := s.BeginGenerate(session, "", profile, instruction, config.Parameters{})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Generating {
		t.Fatal("not generating")
	}
	return work(context.Background())
}
func save(t *testing.T, s *Service, id string, version int, raw bool) View {
	t.Helper()
	_, work, err := s.BeginSave(session, id, version, raw)
	if err != nil {
		t.Fatal(err)
	}
	return work(context.Background())
}

func TestRawSaveOrderNoAIAndPartialRetry(t *testing.T) {
	dst := &destination{fail: map[string]bool{"b": true}}
	s := New(settings(), generatorFunc(func(context.Context, Request) (Result, error) { t.Error("raw must not call AI"); return Result{}, nil }), dst)
	v := add(t, s, Material{ID: 1, Text: "original  #tag", Attachments: []Attachment{{ID: "a"}}})
	add(t, s, Material{ID: 2, Text: "https://source.test\ncaption", Attachments: []Attachment{{ID: "b"}}})
	if dst.creates != 0 || len(dst.uploads) != 0 {
		t.Fatal("wrote while collecting")
	}
	v = save(t, s, v.Group, 0, true)
	if !v.Partial || dst.creates != 1 || dst.contents[0] != "original  #tag\n\nhttps://source.test\ncaption" {
		t.Fatalf("bad partial save: %+v %+v", v, dst)
	}
	if _, err := s.Add(session, Material{ID: 3, Text: "must not vanish"}); err == nil {
		t.Fatal("accepted material during partial save")
	}
	dst.fail["b"] = false
	oldID := v.Group
	v = save(t, s, oldID, 0, true)
	if v.Group != "" || dst.creates != 1 || dst.uploads["a"] != 1 || dst.uploads["b"] != 2 {
		t.Fatalf("retry duplicated successful work: %+v %+v", v, dst)
	}
	if _, _, err := s.BeginSave(session, oldID, 0, true); err == nil {
		t.Fatal("duplicate save accepted")
	}
	add(t, s, Material{ID: 4, Text: "new group"})
	if _, _, err := s.BeginSave(session, oldID, 0, true); err == nil {
		t.Fatal("old group button saved new material")
	}
}

func TestSnapshotRevisionAndModelSwitch(t *testing.T) {
	started, release := make(chan Request, 1), make(chan struct{})
	dst := &destination{}
	s := New(settings(), generatorFunc(func(_ context.Context, r Request) (Result, error) {
		started <- r
		<-release
		return Result{Text: "edited #Go"}, nil
	}), dst)
	v := add(t, s, Material{ID: 1, Text: "source to rewrite", Sources: []string{"https://source.test"}, Attachments: []Attachment{{ID: "old-image"}}})
	_, work, err := s.BeginGenerate(session, v.Group, "tidy", "remove secret paragraph", config.Parameters{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan View, 1)
	go func() { done <- work(context.Background()) }()
	req := <-started
	if len(req.Materials) != 1 {
		t.Fatal("wrong snapshot")
	}
	add(t, s, Material{ID: 2, Text: "new material", Attachments: []Attachment{{ID: "new-image"}}})
	if _, _, err := s.BeginGenerate(session, v.Group, "tidy", "", config.Parameters{}); err == nil {
		t.Fatal("parallel generation accepted")
	}
	close(release)
	v = <-done
	if v.Pending != 1 || v.Draft.Count != 1 || dst.creates != 0 || !strings.Contains(v.Notice, "尚未纳入") {
		t.Fatalf("snapshot not marked: %+v", v)
	}
	if _, err := s.SelectModel(session, "b"); err != nil {
		t.Fatal(err)
	}
	var revision Request
	s.gen = generatorFunc(func(_ context.Context, r Request) (Result, error) {
		revision = r
		return Result{Text: "new draft #Go"}, nil
	})
	v = generate(t, s, "", "only change tags")
	if revision.Draft != "edited #Go" || revision.Model != "b" || len(revision.Requirements) != 2 || revision.Requirements[0] != "remove secret paragraph" || len(revision.Materials) != 2 {
		t.Fatalf("lost editing context: %+v", revision)
	}
	if _, _, err := s.BeginSave(session, v.Group, 1, false); err == nil {
		t.Fatal("stale version saved")
	}
	v = save(t, s, v.Group, 2, false)
	if v.Group != "" || !strings.Contains(dst.contents[0], "https://source.test") || strings.Contains(dst.contents[0], "source to rewrite") || dst.uploads["old-image"] != 1 || dst.uploads["new-image"] != 1 {
		t.Fatalf("wrong final content: %+v", dst)
	}
}

func TestSavingOlderSnapshotRetainsNewMaterial(t *testing.T) {
	s := New(settings(), generatorFunc(func(context.Context, Request) (Result, error) { return Result{Text: "snapshot"}, nil }), &destination{})
	v := add(t, s, Material{ID: 1, Text: "old"})
	_, work, err := s.BeginGenerate(session, v.Group, "tidy", "", config.Parameters{})
	if err != nil {
		t.Fatal(err)
	}
	add(t, s, Material{ID: 2, Text: "new"})
	v = work(context.Background())
	id := v.Group
	v = save(t, s, id, v.Draft.Version, false)
	if v.Count != 1 || !v.Collecting || v.Group == id || s.g.materials[0].Text != "new" {
		t.Fatalf("lost unprocessed material: %+v", v)
	}
}

func TestFailuresKeepContentAndIncompleteDraftCannotSave(t *testing.T) {
	dst := &destination{createFail: true}
	s := New(settings(), generatorFunc(func(context.Context, Request) (Result, error) { return Result{Text: "good"}, nil }), dst)
	v := add(t, s, Material{ID: 1, Text: "source"})
	v = generate(t, s, "tidy", "")
	s.gen = generatorFunc(func(context.Context, Request) (Result, error) { return Result{}, errors.New("offline") })
	v = generate(t, s, "", "revision")
	if v.Draft.Text != "good" || v.Draft.Version != 1 || v.Count != 1 {
		t.Fatal("AI failure destroyed content")
	}
	v = save(t, s, v.Group, 1, false)
	if v.Draft.Text != "good" || v.Saving || v.Count != 1 {
		t.Fatal("Memos failure destroyed content")
	}
	s.gen = generatorFunc(func(context.Context, Request) (Result, error) { return Result{Text: "partial", Truncated: true}, nil })
	v = generate(t, s, "", "")
	if _, _, err := s.BeginSave(session, v.Group, v.Draft.Version, false); err == nil {
		t.Fatal("incomplete output saved")
	}
	if _, err := s.Discard("qq:private:42", v.Group); err == nil {
		t.Fatal("session scope ignored")
	}
	if _, err := s.Discard(session, v.Group); err != nil {
		t.Fatal(err)
	}
	if s.Status(session).Count != 0 {
		t.Fatal("discard did not clear")
	}
}

func TestConcurrentConfirmCreatesOnce(t *testing.T) {
	dst := &destination{}
	s := New(settings(), nil, dst)
	v := add(t, s, Material{Text: "raw"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var jobs []func(context.Context) View
	for range 12 {
		wg.Go(func() {
			_, work, err := s.BeginSave(session, v.Group, 0, true)
			if err == nil {
				mu.Lock()
				jobs = append(jobs, work)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(jobs) != 1 {
		t.Fatalf("accepted %d saves", len(jobs))
	}
	jobs[0](context.Background())
	if dst.creates != 1 {
		t.Fatal("duplicate creation")
	}
}

func TestCollectKeepsDraftAndRestartClears(t *testing.T) {
	cfg := settings()
	dst := &destination{}
	s := New(cfg, generatorFunc(func(context.Context, Request) (Result, error) { return Result{Text: "keep edits"}, nil }), dst)
	add(t, s, Material{ID: 1, Text: "old"})
	v := generate(t, s, "tidy", "")
	if _, err := s.Collect(session, v.Group); err != nil {
		t.Fatal(err)
	}
	v = add(t, s, Material{ID: 2, Text: "typed supplement"})
	if !v.Collecting || v.Draft.Text != "keep edits" || v.Pending != 1 {
		t.Fatalf("bad collect: %+v", v)
	}
	if New(cfg, nil, dst).Status(session).Group != "" {
		t.Fatal("session unexpectedly persisted")
	}
}
