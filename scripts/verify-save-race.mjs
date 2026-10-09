import assert from 'node:assert/strict';
import {access, mkdir, writeFile} from 'node:fs/promises';
import path from 'node:path';
import {randomUUID} from 'node:crypto';
import {chromium} from 'playwright';

const origin = process.env.TEST_ORIGIN || 'http://127.0.0.1:8080';
const resultsDir = path.resolve('test-results');
const projectId = randomUUID();
const sceneId = randomUUID();
const errors = [];
const putRequests = [];
let browser;
let releaseResponse;
let waitForSave;
let holdNextSave = false;
let heldResponse;
let agentRequests = 0;
let created = false;

async function api(endpoint, options = {}) {
  const response = await fetch(new URL(endpoint, origin), {...options, headers: {'Content-Type': 'application/json', ...options.headers}});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `请求失败：${response.status}`);
  return body;
}

async function eventually(check, message) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(message);
}

function holdSave() {
  holdNextSave = true;
  let resolveSaved;
  const saved = new Promise(resolve => {resolveSaved = resolve;});
  heldResponse = new Promise(resolve => {releaseResponse = resolve;});
  waitForSave = resolveSaved;
  return saved;
}

async function externalEdit(revision, name, notes) {
  const value = await api('/api/tools/call', {
    method: 'POST',
    body: JSON.stringify({name: 'video_edit', arguments: {projectId, revision, operations: [
      {type: 'project.update', patch: {name}},
      {type: 'scene.update', sceneId, patch: {notes}},
    ]}}),
  });
  return value.result.project;
}

try {
  await mkdir(resultsDir, {recursive: true});
  const project = await api('/api/projects', {
    method: 'POST',
    body: JSON.stringify({id: projectId, name: '保存竞态独立验证', width: 1280, height: 720, fps: 30, revision: 0, updatedAt: new Date().toISOString(),
      scenes: [{id: sceneId, name: '独立验证镜头', duration: 180, background: '#147D68', notes: '保存与实时同步竞态回归', nodes: [{id: randomUUID(), type: 'text', name: '验证标题', text: '实时观看验证', x: 100, y: 200, width: 1080, height: 200, rotation: 0, opacity: 1, color: '#FFFFFF', fontSize: 64, fontWeight: 600, start: 0, end: 180, animation: 'none', locked: false, hidden: false}]}], audio: []}),
  });
  created = true;
  const chromePath = process.env.CHROME_PATH || '/home/icy/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome';
  let executablePath;
  try {await access(chromePath); executablePath = chromePath;} catch {}
  browser = await chromium.launch({headless: true, executablePath, args: ['--no-sandbox', '--disable-dev-shm-usage']});
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce'});
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', response => {if (response.url().endsWith('/api/projects/' + projectId) && response.status() >= 400) errors.push(`工程保存响应 ${response.status()}`);});
  await page.route('**/api/agent/runs', route => {
    agentRequests++;
    return route.fulfill({status: 400, contentType: 'application/json', body: JSON.stringify({error: '独立验收已拦截模型调用'})});
  });
  for (const endpoint of ['/api/agent', '/api/speech', '/api/images/generate']) {
    await page.route('**' + endpoint, route => {
      errors.push('出现意外模型调用 ' + endpoint);
      return route.fulfill({status: 400, contentType: 'application/json', body: JSON.stringify({error: '验收禁止收费请求'})});
    });
  }
  await page.route('**/api/projects/' + projectId, async route => {
    if (route.request().method() !== 'PUT') return route.continue();
    putRequests.push(route.request().postDataJSON());
    const hold = holdNextSave;
    if (hold) holdNextSave = false;
    const response = await route.fetch();
    if (hold) {
      waitForSave(await response.json());
      await heldResponse;
    }
    await route.fulfill({response});
  });
  await page.goto(origin + '/?project=' + projectId, {waitUntil: 'domcontentloaded'});
  await page.locator('.studio-live').filter({hasText: '实时连接'}).waitFor();
  assert.equal(await page.getByLabel('项目名称').inputValue(), project.name);
  await page.getByRole('button', {name: '人工接管', exact: true}).first().click();
  const recovery = () => page.evaluate(() => JSON.parse(localStorage.getItem('yingxu-recovery') || '{}'));
  const firstSave = holdSave();
  await page.getByLabel('项目名称').fill('人工保存第二版');
  const second = await firstSave;
  await eventually(async () => (await recovery()).revision === second.revision, '自己保存的 SSE 更新没有到达');
  const third = await externalEdit(second.revision, '外部 AI 第三版', '外部 AI 在 HTTP 响应前完成更新');
  await eventually(async () => (await recovery()).revision === third.revision && await page.getByLabel('项目名称').inputValue() === third.name, '更高版本 SSE 没有同步');
  releaseResponse();
  await eventually(async () => await page.locator('.save-state').innerText() === 'AI 已保存', '延迟保存响应改变了最新保存状态');
  await new Promise(resolve => setTimeout(resolve, 1700));
  assert.equal((await recovery()).revision, third.revision, '旧 HTTP 响应使前端工程版本倒退');
  assert.equal(await page.getByLabel('项目名称').inputValue(), third.name, '旧 HTTP 响应覆盖外部 AI 工程');
  assert.equal(putRequests.length, 1, '最新 SSE 已保存后仍重复触发人工保存');
  await page.getByLabel('项目名称').fill('人工在第三版后继续保存');
  await eventually(async () => (await api('/api/projects/' + projectId)).name === '人工在第三版后继续保存', '收到新版 SSE 后后续人工保存失败');
  const fourth = await api('/api/projects/' + projectId);
  assert.equal(fourth.revision, third.revision + 1);
  assert.equal(putRequests[1].revision, third.revision, '后续人工保存没有使用最新修订号');
  await eventually(async () => (await recovery()).revision === fourth.revision, '后续人工保存修订号没有同步');

  await page.getByLabel('向智能助手发送要求').fill('独立保存竞态验收，不调用模型');
  const pendingBeforeStart = holdSave();
  await page.getByLabel('项目名称').fill('开始 AI 任务前人工修改');
  await page.getByRole('button', {name: '发送要求', exact: true}).click();
  const fifth = await pendingBeforeStart;
  await eventually(async () => (await recovery()).revision === fifth.revision, '任务前保存的 SSE 没有同步');
  const sixth = await externalEdit(fifth.revision, '任务前外部 AI 最新版本', '任务前等待保存时外部 AI 更新');
  await eventually(async () => (await recovery()).revision === sixth.revision, '任务前外部 AI 新版本没有同步');
  releaseResponse();
  await eventually(async () => agentRequests === 1, '任务前旧保存响应导致新版工程被误判为未保存');
  await page.getByRole('alert').filter({hasText: '独立验收已拦截模型调用'}).waitFor();
  assert.equal((await recovery()).revision, sixth.revision, '任务前旧保存响应把版本回退');
  await page.getByLabel('项目名称').fill('任务前竞态后再次人工保存');
  await eventually(async () => (await api('/api/projects/' + projectId)).name === '任务前竞态后再次人工保存', '任务前竞态后无法继续人工保存');
  const seventh = await api('/api/projects/' + projectId);
  assert.equal(seventh.revision, sixth.revision + 1);
  assert.equal(putRequests.at(-1).revision, sixth.revision, '任务前竞态后人工保存修订号错误');
  assert.deepEqual(errors, []);
  await page.screenshot({path: path.join(resultsDir, 'save-race.png'), fullPage: true});
  const report = {ok: true, projectId, assertions: ['自动保存旧 HTTP 响应没有覆盖更高 SSE 修订号', '已经保存的新版本没有产生重复 PUT', '后续人工保存使用新 revision 并成功', '开始 AI 前旧保存响应保留最新工程', '任务前保存竞态后可继续编辑'], revisions: [project.revision, second.revision, third.revision, fourth.revision, fifth.revision, sixth.revision, seventh.revision], paidRequests: 0, errors};
  await writeFile(path.join(resultsDir, 'save-race.json'), JSON.stringify(report, null, 2));
  process.stdout.write(JSON.stringify(report, null, 2) + '\n');
} finally {
  releaseResponse?.();
  await browser?.close();
  if (created) await api('/api/projects/' + projectId, {method: 'DELETE'}).catch(() => {});
}
