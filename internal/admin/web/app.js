'use strict';
const $ = id => document.getElementById(id);
let setup = false, data, keys = {}, resetKeys = new Set(), tab = 'providers';
const selected = {};
const sections = {
  providers: ['AI 供应商', '连接你的模型服务，密钥留空时保留现有设置。'],
  models: ['模型', '设置模型 ID、接口协议和常用生成参数。'],
  profiles: ['整理方案', '编辑整理提示词，选择方案使用的模型和图片能力。'],
  general: ['通用设置', '调整系统提示词、标签、素材限制和机器人入口。'],
  password: ['管理密码', '修改网页配置的独立登录密码。']
};
function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else if (key === 'class') node.className = value;
    else node[key] = value;
  }
  for (const child of children.flat()) if (child != null) node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  return node;
}
async function api(path, method = 'GET', body) {
  const response = await fetch('/api/' + path, {method, credentials: 'same-origin', headers: body ? {'Content-Type': 'application/json'} : {}, body: body ? JSON.stringify(body) : undefined});
  const result = await response.json();
  if (!response.ok) {
    if (response.status === 401 && path !== 'login') showAuth(false);
    throw new Error(result.error || '请求失败，请稍后重试');
  }
  return result;
}
function notify(text, error = false) {
  $('notice').hidden = false;
  $('notice').className = error ? 'error' : '';
  $('notice').textContent = text;
}
function showAuth(first) {
  setup = first;
  $('loading').hidden = true; $('app').hidden = true; $('auth').hidden = false;
  $('auth-title').textContent = first ? '设置管理密码' : '登录配置中心';
  $('auth-description').textContent = first ? '首次使用，先设置一个独立密码，再配置你的 AI 服务。' : '使用你的独立管理密码登录。';
  $('auth-submit').textContent = first ? '设置密码并进入' : '登录';
  $('confirm-wrap').hidden = !first;
  $('confirm-password').required = first;
  $('password').autocomplete = first ? 'new-password' : 'current-password';
  $('password').value = ''; $('confirm-password').value = ''; $('auth-error').textContent = '';
  $('password').focus();
}
async function load() {
  const result = await api('settings');
  data = result; keys = {}; resetKeys.clear();
  $('loading').hidden = true; $('auth').hidden = true; $('app').hidden = false;
  render();
}
$('auth-form').addEventListener('submit', async event => {
  event.preventDefault();
  const password = $('password').value;
  if (setup && password !== $('confirm-password').value) { $('auth-error').textContent = '两次输入的密码不一致'; return; }
  $('auth-submit').disabled = true; $('auth-error').textContent = '';
  try { await api(setup ? 'setup' : 'login', 'POST', {password}); await load(); }
  catch (error) { $('auth-error').textContent = error.message; }
  finally { $('auth-submit').disabled = false; }
});
$('logout').addEventListener('click', async () => {
  try { await api('logout', 'POST', {}); data = null; keys = {}; showAuth(false); }
  catch (error) { notify(error.message, true); }
});
$('save').addEventListener('click', async () => {
  for (const input of $('editor').querySelectorAll('input,textarea,select')) if (!input.reportValidity()) return;
  $('save').disabled = true; $('notice').hidden = true;
  try { await api('settings', 'PUT', {notebook: data.notebook, api_keys: keys, reset_keys: [...resetKeys]}); await load(); notify('配置已保存'); }
  catch (error) { notify(error.message, true); }
  finally { $('save').disabled = false; }
});
function field(label, object, key, options = {}) {
  const {type = 'text', help, placeholder = '', rows, min, max, step = 'any', optional = false} = options;
  const input = el(type === 'textarea' ? 'textarea' : 'input', {value: object[key] ?? '', placeholder, oninput: event => {
    object[key] = type === 'number' ? (event.target.value === '' ? null : Number(event.target.value)) : (optional && event.target.value === '' ? null : event.target.value);
  }});
  if (type !== 'textarea') input.type = type;
  if (type === 'number') { input.step = step; if (min != null) input.min = min; if (max != null) input.max = max; }
  if (!optional && type !== 'password') input.required = true;
  if (rows) input.rows = rows;
  if (options.class) input.className = options.class;
  if (type === 'password') input.autocomplete = 'new-password';
  return el('label', options.wide ? {class: 'span-two'} : {}, label, input, help ? el('span', {class: 'help'}, help) : null);
}
function choose(label, object, key, choices, help) {
  const select = el('select', {onchange: event => object[key] = event.target.value});
  for (const [value, text] of choices) select.append(el('option', {value}, text));
  select.value = object[key] ?? '';
  if (!select.value && object[key]) select.prepend(el('option', {value: object[key], selected: true}, object[key] + '（不存在）'));
  return el('label', {}, label, select, help ? el('span', {class: 'help'}, help) : null);
}
function check(label, object, key) { return el('label', {class: 'check'}, el('input', {type: 'checkbox', checked: !!object[key], onchange: event => object[key] = event.target.checked}), label); }
function jsonField(label, object, key) {
  const input = el('textarea', {class: 'code', value: object[key] ? JSON.stringify(object[key], null, 2) : '', placeholder: '{}', oninput: event => {
    try {
      const value = event.target.value.trim() ? JSON.parse(event.target.value) : null;
      if (value != null && (typeof value !== 'object' || Array.isArray(value))) throw new Error('请输入 JSON 对象');
      object[key] = value; event.target.setCustomValidity('');
    } catch (_) { event.target.setCustomValidity('请输入有效的 JSON 对象'); }
  }});
  return el('label', {}, label, input);
}
function panel(title, description, ...children) { return el('section', {class: 'panel'}, title ? el('div', {class: 'panel-heading'}, el('div', {}, el('h2', {}, title), description ? el('p', {}, description) : null)) : null, ...children); }
function advanced(...children) { return el('details', {}, el('summary', {}, '高级参数'), el('div', {class: 'stack'}, ...children)); }
function params(object) {
  object.parameters ||= {};
  const p = object.parameters;
  return el('div', {class: 'stack'},
    el('div', {class: 'grid'}, field('输出 Token 上限', p, 'max_tokens', {type: 'number', min: 1, optional: true, placeholder: '留空：继承 / 不发送'}), field('思考等级', p, 'reasoning_effort', {optional: true, placeholder: '例如 low / medium / high，留空不发送'})),
    advanced(el('div', {class: 'grid'}, field('Temperature', p, 'temperature', {type: 'number', optional: true}), field('Top P', p, 'top_p', {type: 'number', optional: true}), field('思考预算', p, 'reasoning_budget', {type: 'number', min: 1, optional: true}), field('输出详细程度', p, 'verbosity', {optional: true, placeholder: '留空不发送'})), jsonField('供应商扩展参数（JSON）', p, 'extra'))
  );
}
function collection(kind, label, create, edit) {
  const items = data.notebook[kind];
  const names = Object.keys(items).sort();
  if (!items[selected[kind]]) selected[kind] = names[0];
  const select = el('select', {value: selected[kind] || '', onchange: event => { selected[kind] = event.target.value; render(); }});
  for (const name of names) select.append(el('option', {value: name, selected: name === selected[kind]}, name));
  const add = el('button', {onclick: () => {
    const name = window.prompt('新' + label + '的配置名称（字母、数字、下划线或短横线）');
    if (!name) return;
    if (!/^[a-zA-Z0-9_-]+$/.test(name) || name in items) { notify('名称无效或已存在', true); return; }
    items[name] = create(); selected[kind] = name; render();
  }}, '+ 新增' + label);
  const content = [el('div', {class: 'picker'}, select, add)];
  if (!names.length) { content.push(el('div', {class: 'empty'}, '还没有' + label + '，点击上方按钮添加。')); return content; }
  const name = selected[kind], object = items[name];
  content.push(edit(name, object));
  content.push(el('div', {class: 'inline-actions'}, el('button', {class: 'danger', onclick: () => {
    delete items[name]; if (kind === 'providers') { delete keys[name]; resetKeys.add(name); }
    render(); notify('已从编辑内容移除，保存后生效。仍被其他配置引用时会提示修正。');
  }}, '移除这个' + label)));
  return content;
}
function providers() {
  return collection('providers', '供应商', () => ({base_url: '', key_env: '', timeout: 180e9, headers: {}}), (name, p) => {
    const seconds = {timeout: p.timeout / 1e9};
    const timeout = field('请求超时（秒）', seconds, 'timeout', {type: 'number', min: 1});
    timeout.querySelector('input').addEventListener('input', () => p.timeout = seconds.timeout * 1e9);
    const key = el('input', {type: 'password', autocomplete: 'new-password', value: keys[name] || '', placeholder: data.key_set[name] ? '已设置，留空保持不变' : '输入 API Key（无需认证可留空）', oninput: event => { keys[name] = event.target.value; resetKeys.delete(name); }});
    return el('div', {class: 'stack'}, el('div', {class: 'grid'}, field('API 地址', p, 'base_url', {type: 'url', placeholder: 'https://api.example.com/v1', wide: true}), el('label', {}, 'API Key', key), timeout), advanced(field('密钥环境变量名称', p, 'key_env', {optional: true, help: '未在网页保存密钥时，使用这个环境变量。'}), el('div', {class: 'grid'}, field('供应商专用代理', p, 'proxy_url', {optional: true, placeholder: '留空继承，direct 为直连'}), field('代理环境变量名称', p, 'proxy_env', {optional: true})), jsonField('额外请求头（JSON）', p, 'headers'), el('button', {onclick: () => { resetKeys.add(name); delete keys[name]; key.value = ''; notify('保存后清除网页密钥，改用环境变量。'); }}, '改用环境变量密钥')));
  });
}
function models() {
  return collection('models', '模型', () => ({provider: Object.keys(data.notebook.providers)[0] || '', id: '', protocol: 'responses', vision: false, parameters: {}}), (_, m) => el('div', {class: 'stack'},
    el('div', {class: 'grid'}, choose('供应商', m, 'provider', Object.keys(data.notebook.providers).sort().map(x => [x,x])), field('实际模型 ID', m, 'id', {placeholder: '供应商提供的模型名称'}), choose('接口协议', m, 'protocol', [['responses','Responses'],['chat_completions','Chat Completions']]), field('Chat 输出上限字段', m, 'token_field', {optional: true, placeholder: '通常留空；部分服务使用 max_tokens'})),
    el('div', {class: 'checks'}, check('支持图片理解', m, 'vision')), params(m), advanced(field('原生思考预算字段', m, 'reasoning_budget_field', {optional: true, placeholder: '例如 thinking.budget_tokens'}))
  ));
}
function profiles() {
  return collection('profiles', '方案', () => ({name: '新整理方案', model: Object.keys(data.notebook.models)[0] || '', prompt: '', images: false, tags: true, parameters: {}}), (_, p) => el('div', {class: 'stack'},
    el('div', {class: 'grid'}, field('方案名称', p, 'name'), choose('默认模型', p, 'model', Object.keys(data.notebook.models).sort().map(x => [x,x]))),
    field('整理提示词', p, 'prompt', {type: 'textarea', class: 'prompt'}), el('div', {class: 'checks'}, check('发送图片给模型', p, 'images'), check('生成标签', p, 'tags')), params(p)
  ));
}
function general() {
  const n = data.notebook;
  const limits = {status: n.status_every / 1e9};
  const status = field('状态提示间隔（秒）', limits, 'status', {type: 'number', min: 1});
  status.querySelector('input').addEventListener('input', () => n.status_every = limits.status * 1e9);
  const tags = {text: (n.common_tags || []).join('，')};
  const tagField = field('常用标签', tags, 'text', {optional: true, help: '用逗号分隔'});
  tagField.querySelector('input').addEventListener('input', () => n.common_tags = tags.text.split(/[,，]/).map(s => s.trim()).filter(Boolean));
  const entryRows = el('div');
  for (const key of Object.keys(n.entries).sort()) {
    const entry = n.entries[key];
    entryRows.append(el('div', {class: 'entry'}, el('label', {}, '入口', el('input', {value: key, readOnly: true})), field('按钮文字', entry, 'label'), choose('操作', entry, 'action', [['raw','原样保存'],['generate','AI 整理']]), choose('整理方案', entry, 'profile', [['','无'], ...Object.keys(n.profiles).map(x => [x,x])]), check('自定义', entry, 'custom'), el('button', {class: 'danger', onclick: () => { delete n.entries[key]; render(); }}, '移除')));
  }
  return [panel('全局提示词', '所有整理方案共用的基础要求。', el('div', {class: 'stack'}, field('系统提示词', n, 'system_prompt', {type: 'textarea', class: 'prompt'}), field('标签提示词', n, 'tag_prompt', {type: 'textarea'}), tagField)),
    panel('素材限制', '按需要调整图片数量和文本容量。', el('div', {class: 'grid'}, field('每次最多读取图片数', n, 'max_images', {type: 'number', min: 1}), field('最多素材条数', n, 'max_materials', {type: 'number', min: 1}), field('文本总量上限（字节）', n, 'max_text_bytes', {type: 'number', min: 1}), status)),
    panel('机器人入口', '将数字入口映射到整理方案。', entryRows, el('button', {onclick: () => { const key = window.prompt('新入口（例如 6）'); if (!key) return; if (key in n.entries || /[\s:]/.test(key) || key.length > 12) { notify('入口无效或已存在', true); return; } n.entries[key] = {label: '新整理', action: 'generate', profile: Object.keys(n.profiles)[0] || '', custom: false}; render(); }}, '+ 新增入口'))];
}
function password() {
  const values = {old_password: '', password: '', confirm: ''};
  const form = el('form', {class: 'password-form', onsubmit: async event => {
    event.preventDefault();
    if (values.password !== values.confirm) { notify('两次输入的新密码不一致', true); return; }
    const button = form.querySelector('button'); button.disabled = true;
    try { await api('password', 'POST', {old_password: values.old_password, password: values.password}); render(); notify('管理密码已更新'); }
    catch (error) { notify(error.message, true); }
    finally { button.disabled = false; }
  }}, field('原密码', values, 'old_password', {type: 'password'}), field('新密码', values, 'password', {type: 'password', help: '8～72 字节'}), field('再次输入新密码', values, 'confirm', {type: 'password'}), el('button', {class: 'primary', type: 'submit'}, '修改密码'));
  return panel('修改管理密码', '密码更新后，其他已登录的会话需要重新登录。', form);
}
function render() {
  $('navigation').replaceChildren(...Object.entries(sections).map(([key, [title]], index) => el('button', {class: key === tab ? 'active' : '', onclick: () => { tab = key; $('notice').hidden = true; render(); }}, el('span', {class: 'nav-number'}, '0' + (index+1)), title)));
  $('page-title').textContent = sections[tab][0]; $('page-description').textContent = sections[tab][1]; $('save').hidden = tab === 'password';
  const result = {providers, models, profiles, general, password}[tab]();
  $('editor').replaceChildren(...(Array.isArray(result) ? (tab === 'general' ? result : [panel('编辑' + {providers:'供应商',models:'模型',profiles:'整理方案'}[tab], '', ...result)]) : [result]));
}
api('status').then(status => status.authenticated ? load() : showAuth(status.setup_required)).catch(error => { $('loading').textContent = error.message; });
