import assert from 'node:assert/strict';
import {mkdir, writeFile, access} from 'node:fs/promises';
import path from 'node:path';
import {randomUUID} from 'node:crypto';
import {chromium} from 'playwright';

const origin = process.env.TEST_ORIGIN || 'http://127.0.0.1:8080';
const resultsDir = path.resolve('test-results');
const ids = new Set();
const browserErrors = [];
const failedRequests = [];
const checkpoints = [];
const expectedHttpErrors = [];
let browser;
let testFailure;

async function api(endpoint, options = {}) {
  const response = await fetch(new URL(endpoint, origin), {...options, headers: {'Content-Type': 'application/json', ...options.headers}});
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败：${response.status}`);
  return data;
}

async function eventually(check, message) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 150));
  }
  throw new Error(message);
}

async function takeControl(page) {
  await page.getByRole('button', {name: '人工接管', exact: true}).first().click();
  await page.getByRole('button', {name: '返回实时观看', exact: true}).waitFor();
}

async function protectPaidRequests(page) {
  await page.route('**/api/agent/runs', route => {
    assert.equal(route.request().method(), 'POST', '任务创建接口必须使用 POST');
    return route.fulfill({status: 400, contentType: 'application/json', body: JSON.stringify({error: '尚未配置文本模型，请填写支持工具调用的模型地址与模型名称'})});
  });
  for (const endpoint of ['/api/agent', '/api/images/generate', '/api/speech']) {
    await page.route('**' + endpoint, route => {
      browserErrors.push('验收出现意外模型调用：' + endpoint);
      return route.fulfill({status: 400, contentType: 'application/json', body: JSON.stringify({error: '验收禁止调用远程模型'})});
    });
  }
}

async function studioTool(name, argumentsValue) {
  const response = await api('/api/tools/call', {method: 'POST', body: JSON.stringify({name, arguments: argumentsValue})});
  return response.result;
}

const sceneId = randomUUID();
const textId = randomUUID();
const secondSceneId = randomUUID();
const audioId = randomUUID();
const audioPath = '/uploads/browser-verification.wav';
const samples = 16000;
const waveform = Buffer.alloc(44 + samples * 2);
waveform.write('RIFF', 0);waveform.writeUInt32LE(waveform.length - 8, 4);waveform.write('WAVE', 8);waveform.write('fmt ', 12);waveform.writeUInt32LE(16, 16);waveform.writeUInt16LE(1, 20);waveform.writeUInt16LE(1, 22);waveform.writeUInt32LE(samples, 24);waveform.writeUInt32LE(samples * 2, 28);waveform.writeUInt16LE(2, 32);waveform.writeUInt16LE(16, 34);waveform.write('data', 36);waveform.writeUInt32LE(samples * 2, 40);
const project = {
  id: randomUUID(), name: '浏览器自动验证工程', width: 1280, height: 720, fps: 30, revision: 0, updatedAt: new Date().toISOString(),
  scenes: [{id: sceneId, name: '验证镜头', duration: 90, background: '#F2F5F4', notes: '浏览器验收用独立工程', nodes: [
    {id: randomUUID(), type: 'rect', name: '验证矩形', x: 70, y: 90, width: 1130, height: 520, rotation: 0, opacity: 1, color: '#147D68', start: 0, end: 90, animation: 'none', locked: false, hidden: false},
    {id: textId, type: 'text', name: '验证标题图层', x: 120, y: 230, width: 970, height: 190, rotation: 0, opacity: 1, color: '#FFFFFF', text: '浏览器验证标题', fontSize: 66, fontWeight: 600, start: 6, end: 60, animation: 'none', locked: false, hidden: false}
  ]}, {id: secondSceneId, name: '第二个验证镜头', duration: 90, background: '#C5E7DD', notes: '', nodes: []}],
  audio: [{id: audioId, name: '验证音轨', src: audioPath, start: 6, trimStart: 0, duration: 18, sourceDuration: 30, volume: .5, fadeIn: 0, fadeOut: 0}]
};

async function timelineRegressions() {
  const sceneId = randomUUID(), earlyId = randomUUID(), lateId = randomUUID(), partialId = randomUUID(), outsideId = randomUUID();
  const node = (id, name, start, end, y) => ({id, type: 'text', name, text: name, x: 80, y, width: 900, height: 100, rotation: 0, opacity: 1, color: '#FFFFFF', fontSize: 50, fontWeight: 600, start, end, animation: 'none', locked: false, hidden: false});
  const fixture = {
    id: randomUUID(), name: '时间线裁剪边界验证', width: 1280, height: 720, fps: 30, revision: 0, updatedAt: new Date().toISOString(),
    scenes: [{id: sceneId, name: '边界验证镜头', duration: 180, background: '#147D68', notes: '', nodes: [node(earlyId, '开头验证图层', 10, 50, 120), node(lateId, '尾部验证图层', 150, 180, 350)]}],
    audio: [{id: partialId, name: '部分越界音轨', src: audioPath, start: 120, trimStart: 0, duration: 60, sourceDuration: 60, volume: .5, fadeIn: 50, fadeOut: 45}, {id: outsideId, name: '完全越界音轨', src: audioPath, start: 160, trimStart: 0, duration: 20, sourceDuration: 20, volume: .5, fadeIn: 10, fadeOut: 10}]
  };
  const saved = await api('/api/projects', {method: 'POST', body: JSON.stringify(fixture)});
  ids.add(saved.id);
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
  page.on('pageerror', error => browserErrors.push(error.message));
  page.on('console', message => {if (message.type() === 'error') browserErrors.push(message.text());});
  page.on('requestfailed', request => {if (!request.failure()?.errorText?.includes('ERR_ABORTED')) failedRequests.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText}`);});
  await protectPaidRequests(page);
  await page.route('**' + audioPath, route => route.fulfill({status: 200, contentType: 'audio/wav', body: waveform}));
  await page.addInitScript(value => localStorage.setItem('yingxu-recovery', JSON.stringify(value)), saved);
  const read = () => api('/api/projects/' + saved.id);
  const geometry = async edge => {
    const handle = page.getByRole('button', {name: `裁剪边界验证镜头${edge === 'left' ? '开始' : '结束'}时间`, exact: true});
    await handle.scrollIntoViewIfNeeded();
    const bounds = await handle.boundingBox();
    const track = await page.locator('.scene-clip').locator('..').boundingBox();
    assert.ok(bounds && track, '镜头裁剪控件不可见');
    return {handle, x: bounds.x + bounds.width / 2, y: bounds.y + bounds.height / 2, width: track.width};
  };
  const dragFrames = async (edge, delta) => {
    const {x, y, width} = await geometry(edge);
    const duration = (await read()).scenes[0].duration;
    await page.mouse.move(x, y);await page.mouse.down();
    await page.mouse.move(x + width / duration * delta, y, {steps: 8});
    await page.mouse.up();
  };
  const restore = async () => {
    await page.getByRole('button', {name: '撤销', exact: true}).click();
    await eventually(async () => {
      const value = await read();
      return value.scenes[0].duration === 180 && value.scenes[0].nodes.length === 2 && value.audio.length === 2 && value.audio.find(track => track.id === partialId)?.duration === 60;
    }, '撤销没有恢复镜头和原始音轨');
  };
  try {
    await page.goto(origin, {waitUntil: 'domcontentloaded'});
    await page.getByRole('button', {name: '镜头片段 边界验证镜头', exact: true}).waitFor();
    await takeControl(page);
    await dragFrames('right', -30);
    await eventually(async () => (await read()).scenes[0].duration === 150, '右裁剪没有保存为 150 帧');
    const trimmed = await read();
    assert.equal(trimmed.scenes[0].nodes.some(item => item.id === lateId), false, '完全被裁掉图层没有删除');
    assert.equal(trimmed.audio.some(track => track.id === outsideId), false, '完全越界音轨没有删除');
    const partial = trimmed.audio.find(track => track.id === partialId);
    assert.ok(partial);
    assert.equal(partial.duration, 30, '部分越界音轨没有裁到工程结尾');
    assert.equal(partial.fadeIn, 30, '淡入时间没有限制到裁后片段');
    assert.equal(partial.fadeOut, 30, '淡出时间没有限制到裁后片段');
    await page.getByRole('slider', {name: '播放位置', exact: true}).focus();
    await page.keyboard.press('End');
    assert.equal(await page.locator(`.stage-area .scene-node[data-node-id="${lateId}"]`).count(), 0, '裁后末帧仍出现完全被裁掉图层');
    await page.screenshot({path: path.join(resultsDir, 'timeline-trim-regression.png'), fullPage: true});
    await restore();
    const restored = await read();
    assert.deepEqual(restored.audio, saved.audio, '撤销没有恢复完整音轨与淡入淡出');
    checkpoints.push('镜头右裁剪删除完整越界图层和音轨、裁尾部音轨与淡入淡出、末帧无闪现、撤销恢复');

    const left = await geometry('left');
    await left.handle.focus();
    await page.keyboard.press('Shift+ArrowLeft');
    await eventually(async () => (await read()).scenes[0].duration === 210, '键盘左扩展没有增加 30 帧');
    const keyExtended = await read();
    assert.deepEqual(keyExtended.scenes[0].nodes.map(item => ({id: item.id, start: item.start, end: item.end})), saved.scenes[0].nodes.map(item => ({id: item.id, start: item.start + 30, end: item.end + 30})), '键盘左扩展没有整体后移图层');
    await restore();
    await dragFrames('left', -30);
    await eventually(async () => (await read()).scenes[0].duration === 210, '鼠标左扩展没有增加 30 帧');
    const mouseExtended = await read();
    assert.deepEqual(mouseExtended.scenes[0].nodes, keyExtended.scenes[0].nodes, '键盘与鼠标左扩展后的图层不一致');
    await restore();
    checkpoints.push('键盘与鼠标左扩展镜头时图层一致后移');

    const {x, y, width} = await geometry('right');
    await page.mouse.move(x, y);await page.mouse.down();
    await page.mouse.move(x - width / 180 * 30, y, {steps: 8});
    await page.waitForFunction(id => {
      const saved = JSON.parse(localStorage.getItem('yingxu-recovery') || '{}');
      return saved.id === id && saved.scenes[0].duration === 150 && saved.audio.length === 1;
    }, saved.id);
    await page.mouse.move(x, y, {steps: 8});
    await page.mouse.up();
    await page.waitForFunction(id => {
      const saved = JSON.parse(localStorage.getItem('yingxu-recovery') || '{}');
      return saved.id === id && saved.scenes[0].duration === 180 && saved.audio.length === 2;
    }, saved.id);
    await eventually(async () => {
      const value = await read();
      return value.scenes[0].duration === 180 && value.audio.length === 2 && value.audio.find(track => track.id === partialId)?.duration === 60;
    }, '同次拖动缩短再伸长后原音轨没有恢复并保存');
    const backToOriginal = await read();
    assert.deepEqual(backToOriginal.audio, saved.audio, '同次拖动缩短再伸长后音轨参数被永久裁掉');
    assert.deepEqual(backToOriginal.scenes[0].nodes, saved.scenes[0].nodes, '同次拖动缩短再伸长后原图层被永久裁掉');
    checkpoints.push('同次拖动先缩再伸恢复原图层和音轨、保存成功');
  } finally {await page.close();}
}

try {
  await mkdir(resultsDir, {recursive: true});
  const health = await api('/api/health');
  assert.equal(health.ok, true);
  const saved = await api('/api/projects', {method: 'POST', body: JSON.stringify(project)});
  ids.add(saved.id);
  const requestedPath = process.env.CHROME_PATH || '/home/icy/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome';
  let executablePath;
  try {await access(requestedPath); executablePath = requestedPath;} catch {}
  browser = await chromium.launch({headless: true, executablePath, args: ['--no-sandbox', '--disable-dev-shm-usage']});
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce', colorScheme: 'light'});
  page.on('pageerror', error => browserErrors.push(error.message));
  page.on('console', message => {if (message.type() === 'error') {if (message.location().url.includes('/api/agent') && message.text().includes('400')) expectedHttpErrors.push(message.text()); else browserErrors.push(message.text());}});
  page.on('requestfailed', request => {if (!request.failure()?.errorText?.includes('ERR_ABORTED')) failedRequests.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText}`);});
  await page.addInitScript(value => localStorage.setItem('yingxu-recovery', JSON.stringify(value)), saved);
  await page.route('**/api/settings', route => {
    assert.equal(route.request().method(), 'GET', '浏览器验收不能更改真实模型设置');
    return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({baseUrl: '', model: '', hasApiKey: false, imageBaseUrl: '', imageModel: '', hasImageApiKey: false, ttsProvider: 'local', ttsVoice: 'cmn', ttsSpeed: 1})});
  });
  const modelsRequests = [];
  await page.route('**/api/models', route => {
    const {kind} = route.request().postDataJSON();
    modelsRequests.push(kind);
    return route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({models: [{id: `${kind}-verification-model`, name: '验证模型'}]})});
  });
  await page.route('**' + audioPath, route => route.fulfill({status: 200, contentType: 'audio/wav', body: waveform}));
  await protectPaidRequests(page);
  await page.goto(origin, {waitUntil: 'domcontentloaded'});
  await page.getByLabel('项目名称').waitFor();
  await page.locator('.studio-live').filter({hasText: '实时连接'}).waitFor();
  assert.equal(await page.getByLabel('项目名称').inputValue(), saved.name);
  assert.equal(await page.getByLabel('项目名称').getAttribute('readonly'), '', '默认观看状态允许修改工程名称');
  assert.equal(await page.locator('.stage-area .scene-node[data-node-id="' + textId + '"]').count(), 1);
  await page.screenshot({path: path.join(resultsDir, 'desktop.png'), fullPage: true});
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, '桌面工作台存在横向溢出');
  checkpoints.push('桌面工作台、工程加载和浏览器无溢出');

  const originalCanvasColors = await page.locator('.stage-area .scene-canvas').evaluate(element => ({background: getComputedStyle(element).backgroundColor, text: getComputedStyle(element.querySelector('.scene-text')).color}));
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'light', '默认没有跟随亮色系统');
  await page.emulateMedia({colorScheme: 'dark'});
  await eventually(async () => await page.locator('html').getAttribute('data-theme') === 'dark', '暗色系统偏好没有生效');
  assert.deepEqual(await page.locator('.stage-area .scene-canvas').evaluate(element => ({background: getComputedStyle(element).backgroundColor, text: getComputedStyle(element.querySelector('.scene-text')).color})), originalCanvasColors, '暗色模式改变了工程画布颜色');
  await page.screenshot({path: path.join(resultsDir, 'desktop-dark.png'), fullPage: true});
  await page.getByRole('button', {name: '切换到亮色模式', exact: true}).click();
  assert.equal(await page.evaluate(() => localStorage.getItem('yingxu-theme')), 'light');
  await page.emulateMedia({colorScheme: 'light'});
  await page.emulateMedia({colorScheme: 'dark'});
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'light', '手动亮色被系统暗色覆盖');
  await page.reload({waitUntil: 'domcontentloaded'});
  await page.locator('.studio-live').filter({hasText: '实时连接'}).waitFor();
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'light', '刷新后没有保留手动主题');
  await page.getByRole('button', {name: '切换到暗色模式', exact: true}).click();
  await page.getByRole('button', {name: '模型设置', exact: true}).first().click();
  await page.getByLabel('接口地址', {exact: true}).waitFor();
  const darkModal = await page.locator('dialog').evaluate(element => ({background: getComputedStyle(element).backgroundColor, input: getComputedStyle(element.querySelector('input')).backgroundColor}));
  assert.equal(darkModal.background, 'rgb(25, 34, 28)', '暗色弹窗表面没有生效');
  assert.equal(darkModal.input, 'rgb(19, 28, 22)', '暗色输入表面没有生效');
  await page.screenshot({path: path.join(resultsDir, 'desktop-dark-settings.png'), fullPage: true});
  await page.keyboard.press('Escape');
  await page.getByRole('button', {name: '切换到亮色模式', exact: true}).click();
  checkpoints.push('主题跟随系统、手动记忆、刷新保留、暗色弹窗输入、画布素材颜色保持');

  const original = await api('/api/projects/' + saved.id);
  const updated = await studioTool('video_edit', {projectId: saved.id, revision: original.revision, operations: [{type: 'scene.update', sceneId, patch: {notes: '实时剪辑工具验收已执行'}}]});
  assert.equal(updated.project.scenes[0].notes, '实时剪辑工具验收已执行');
  await page.locator('.studio-step').filter({hasText: '编辑时间线'}).filter({hasText: 'AI 已完成 1 个剪辑操作'}).waitFor();
  await eventually(async () => await page.locator('.canvas-bottom-caption').innerText() === '实时剪辑工具验收已执行', '工具剪辑没有实时更新画布');
  const beforePreview = await page.locator('.timecode').innerText();
  await studioTool('video_preview', {projectId: saved.id, frame: 20});
  await page.locator('.studio-step').filter({hasText: '检查画面'}).filter({hasText: 'AI 正在查看第 20 帧'}).waitFor();
  await eventually(async () => await page.locator('.timecode').innerText() !== beforePreview, '工具预览没有实时定位播放位置');
  await studioTool('video_preview', {projectId: saved.id, frame: 45});
  checkpoints.push('默认观看只读、真实工具剪辑和预览事件实时更新，未调用模型');
  await takeControl(page);

  await page.locator('.stage-area .scene-node[data-node-id="' + textId + '"]').click();
  const textInput = page.getByLabel('文字内容', {exact: true});
  await textInput.fill('已修改的验证标题');
  await eventually(async () => await textInput.inputValue() === '已修改的验证标题', '文字输入没有应用');
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await eventually(async () => await textInput.inputValue() === '浏览器验证标题', '撤销没有恢复文字');
  await page.getByRole('button', {name: '重做', exact: true}).click();
  await eventually(async () => await textInput.inputValue() === '已修改的验证标题', '重做没有恢复修改');
  await eventually(async () => (await api('/api/projects/' + saved.id)).scenes[0].nodes.find(node => node.id === textId).text === '已修改的验证标题', '文字编辑没有成功保存');
  await page.reload({waitUntil: 'domcontentloaded'});
  await takeControl(page);
  await page.locator('.stage-area .scene-node[data-node-id="' + textId + '"]').click();
  assert.equal(await page.getByLabel('文字内容', {exact: true}).inputValue(), '已修改的验证标题');
  const afterReload = await api('/api/projects/' + saved.id);
  await new Promise(resolve => setTimeout(resolve, 1400));
  assert.equal((await api('/api/projects/' + saved.id)).revision, afterReload.revision, '未编辑的工程持续触发保存');
  checkpoints.push('文字编辑、撤销、重做、自动保存与重新加载');

  const drag = async (locator, dx, position = .5) => {
    await locator.scrollIntoViewIfNeeded();
    const box = await locator.boundingBox();
    assert.ok(box, '拖动目标没有显示');
    const x = box.x + box.width * position, y = box.y + box.height / 2;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x + dx, y, {steps: 8});
    await page.mouse.up();
  };
  const savedNode = async () => (await api('/api/projects/' + saved.id)).scenes.find(scene => scene.id === sceneId).nodes.find(node => node.id === textId);
  const savedAudio = async () => (await api('/api/projects/' + saved.id)).audio.find(track => track.id === audioId);
  const firstClip = page.getByRole('button', {name: '镜头片段 验证镜头', exact: true});
  const secondClip = page.getByRole('button', {name: '镜头片段 第二个验证镜头', exact: true});
  const firstBox = await firstClip.boundingBox(), secondBox = await secondClip.boundingBox();
  assert.ok(firstBox && secondBox);
  await drag(firstClip, secondBox.x + secondBox.width / 2 - firstBox.x - firstBox.width / 2);
  await eventually(async () => (await api('/api/projects/' + saved.id)).scenes[0].id === secondSceneId, '镜头拖动没有改变顺序');
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await eventually(async () => (await api('/api/projects/' + saved.id)).scenes[0].id === sceneId, '镜头排序撤销没有保存');
  await page.getByRole('button', {name: '镜头片段 验证镜头', exact: true}).click();
  await drag(page.getByRole('button', {name: '裁剪验证镜头结束时间', exact: true}), -28);
  await eventually(async () => (await api('/api/projects/' + saved.id)).scenes[0].duration < 90, '镜头结束裁剪没有保存');
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await eventually(async () => (await api('/api/projects/' + saved.id)).scenes[0].duration === 90, '镜头裁剪撤销没有保存');
  await drag(page.getByRole('button', {name: '图层片段 验证标题图层', exact: true}), 24);
  await eventually(async () => (await savedNode()).start > 6, '图层片段移动没有保存');
  const nodeAfterMove = await savedNode();
  await drag(page.getByRole('button', {name: '裁剪验证标题图层结束时间', exact: true}), -12);
  await eventually(async () => (await savedNode()).end < nodeAfterMove.end, '图层片段裁剪没有保存');
  await drag(page.getByRole('button', {name: '音频片段 验证音轨', exact: true}), 24);
  await eventually(async () => (await savedAudio()).start > 6, '音频片段移动没有保存');
  await drag(page.getByRole('button', {name: '裁剪验证音轨开始时间', exact: true}), 10);
  await eventually(async () => (await savedAudio()).trimStart > 0 && (await savedAudio()).duration < 18, '音频片段裁剪没有保存');
  checkpoints.push('镜头排序与裁剪、图层移动与裁剪、音频移动与裁剪');

  await timelineRegressions();

  const settingsButton = page.getByRole('button', {name: '模型设置', exact: true}).first();
  await settingsButton.click();
  const dialog = page.locator('dialog');
  await dialog.getByLabel('接口地址', {exact: true}).waitFor();
  const closeButton = dialog.getByRole('button', {name: '关闭弹窗'});
  const saveButton = dialog.getByRole('button', {name: '保存设置'});
  await saveButton.focus();
  await page.keyboard.press('Tab');
  assert.equal(await closeButton.evaluate(element => document.activeElement === element), true, '弹窗向前焦点循环失效');
  await closeButton.focus();
  await page.keyboard.press('Shift+Tab');
  assert.equal(await saveButton.evaluate(element => document.activeElement === element), true, '弹窗向后焦点循环失效');
  await dialog.getByLabel('接口地址', {exact: true}).fill('https://api.example.com/v1');
  await dialog.getByLabel('模型名称', {exact: true}).fill('手动文本模型');
  await dialog.getByRole('button', {name: '刷新模型列表'}).click();
  await dialog.getByRole('combobox', {name: '选择模型'}).waitFor();
  assert.equal(await dialog.getByLabel('模型名称', {exact: true}).inputValue(), '手动文本模型', '刷新覆盖了手动模型');
  await dialog.getByRole('combobox', {name: '选择模型'}).selectOption('text-verification-model');
  assert.equal(await dialog.getByLabel('模型名称', {exact: true}).inputValue(), 'text-verification-model');
  await dialog.getByRole('button', {name: '生图模型', exact: true}).click();
  await dialog.getByLabel('接口地址', {exact: true}).fill('https://images.example.com/v1');
  await dialog.getByRole('button', {name: '刷新模型列表'}).click();
  await dialog.getByRole('combobox', {name: '选择模型'}).selectOption('image-verification-model');
  await dialog.getByRole('button', {name: '语音模型', exact: true}).click();
  assert.equal(await dialog.getByLabel('接口地址', {exact: true}).count(), 0, '本地语音显示了远程接口设置');
  await dialog.getByLabel('语音来源', {exact: true}).selectOption('mimo');
  assert.equal(await dialog.getByLabel('接口地址', {exact: true}).inputValue(), 'https://api.xiaomimimo.com/v1');
  assert.equal(await dialog.getByLabel('模型名称', {exact: true}).inputValue(), 'mimo-v2.5-tts');
  await dialog.getByRole('button', {name: '刷新模型列表'}).click();
  await dialog.getByRole('combobox', {name: '选择模型'}).selectOption('tts-verification-model');
  await dialog.getByLabel('语音来源', {exact: true}).selectOption('openai');
  assert.equal(await dialog.getByLabel('接口地址', {exact: true}).inputValue(), 'https://api.openai.com/v1');
  assert.equal(await dialog.getByLabel('模型名称', {exact: true}).inputValue(), 'gpt-4o-mini-tts');
  assert.equal(await dialog.getByLabel('默认音色', {exact: true}).inputValue(), 'alloy');
  assert.deepEqual(modelsRequests, ['text', 'image', 'tts']);
  checkpoints.push('文本、生图和语音模型列表，本地语音与供应商默认值');
  await page.keyboard.press('Escape');
  assert.equal(await dialog.count(), 0);
  assert.equal(await settingsButton.evaluate(element => document.activeElement === element), true, '弹窗关闭后焦点没有恢复');
  await page.getByRole('tab', {name: '智能助手'}).click();
  await page.getByLabel('向智能助手发送要求').fill('修改当前镜头标题');
  await page.getByRole('button', {name: '发送要求', exact: true}).click();
  await page.locator('.chat-message.error').filter({hasText: '尚未配置文本模型'}).waitFor();
  checkpoints.push('模型设置焦点循环、Escape 与新任务接口的未配置模型错误');

  await page.getByRole('button', {name: '新建工程', exact: true}).first().click();
  await page.getByLabel('工程名称').fill('浏览器验证竖屏工程');
  await page.getByRole('button', {name: /9:16/}).click();
  const createdResponse = page.waitForResponse(response => response.url() === new URL('/api/projects', origin).href && response.request().method() === 'POST');
  await page.getByRole('button', {name: '创建工程', exact: true}).click();
  const created = await (await createdResponse).json();
  assert.ok(created.id, '新工程创建失败');
  ids.add(created.id);
  assert.equal(created.width, 1080);
  assert.equal(created.height, 1920);
  await eventually(async () => await page.locator('dialog').count() === 0, '创建成功后弹窗没有关闭');
  assert.equal(await page.getByLabel('项目名称').inputValue(), '浏览器验证竖屏工程');
  checkpoints.push('独立竖屏工程创建');

  const importedFixture = {...project, name: '浏览器验证导入工程'};
  const importedResponse = page.waitForResponse(response => response.url() === new URL('/api/projects', origin).href && response.request().method() === 'POST');
  await page.locator('input[type=file][accept*=".json"]').setInputFiles({name: '验证工程.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(importedFixture))});
  const imported = await (await importedResponse).json();
  assert.ok(imported.id, '工程导入失败');
  ids.add(imported.id);
  await eventually(async () => (await page.getByLabel('项目名称').inputValue()).includes('浏览器验证导入工程'), '导入后没有打开工程');
  checkpoints.push('JSON 工程导入与打开');

  let bundleSnapshot;
  await page.route('**/api/bundle', route => {
    bundleSnapshot = route.request().postDataJSON().project;
    return route.fulfill({status: 200, contentType: 'application/zip', body: Buffer.from([0x50, 0x4b, 0x05, 0x06, ...new Array(18).fill(0)])});
  });
  await page.getByRole('button', {name: '查看场景数据', exact: true}).click();
  const bundleDownload = page.waitForEvent('download');
  await page.getByRole('button', {name: '下载工程包', exact: true}).click();
  const download = await bundleDownload;
  assert.ok(download.suggestedFilename().endsWith('.zip'));
  assert.equal(bundleSnapshot.id, imported.id, '工程包没有使用当前快照');
  await page.keyboard.press('Escape');
  checkpoints.push('工程包按钮、当前项目快照与 ZIP 下载反馈');

  await page.setViewportSize({width: 375, height: 812});
  await page.getByRole('button', {name: '切换到暗色模式', exact: true}).click();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, '375px 工作台存在横向溢出');
  assert.equal(await page.getByRole('button', {name: '切换到亮色模式', exact: true}).isVisible(), true, '移动端主题按钮不可见');
  await page.screenshot({path: path.join(resultsDir, 'mobile.png'), fullPage: true});
  await page.getByRole('button', {name: '模型设置', exact: true}).first().click();
  await page.getByLabel('接口地址', {exact: true}).waitFor();
  const bounds = await dialog.boundingBox();
  assert.ok(bounds && bounds.x >= 0 && bounds.x + bounds.width <= 375, '375px 模型设置弹窗超出视口');
  await page.screenshot({path: path.join(resultsDir, 'mobile-settings.png'), fullPage: true});
  await page.keyboard.press('Escape');
  checkpoints.push('375px 暗色工作台、主题按钮和设置弹窗');

  assert.deepEqual(browserErrors, [], '浏览器出现错误');
  assert.deepEqual(failedRequests, [], '浏览器出现失败请求');
} catch (error) {
  testFailure = error;
} finally {
  await browser?.close();
  const cleanupErrors = [];
  for (const id of ids) {
    try {await api('/api/projects/' + id, {method: 'DELETE'});} catch (error) {cleanupErrors.push(error.message);}
  }
  await mkdir(resultsDir, {recursive: true});
  if (cleanupErrors.length && !testFailure) testFailure = new Error(cleanupErrors.join('；'));
  await writeFile(path.join(resultsDir, 'browser-report.json'), JSON.stringify({passed: !testFailure, checkpoints, browserErrors, expectedHttpErrors, failedRequests, cleanupErrors, error: testFailure?.message || null, stack: testFailure?.stack || null}, null, 2));
}

if (testFailure) {
  console.error('浏览器验收失败：' + testFailure.message);
  process.exitCode = 1;
} else console.log('浏览器验收通过：' + checkpoints.join('；'));
