import assert from 'node:assert/strict';
import {mkdtemp, mkdir, writeFile, readFile, access, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {createServer} from 'node:net';
import {randomUUID} from 'node:crypto';

const root = path.resolve(import.meta.dirname, '..');
const temporary = await mkdtemp(path.join(tmpdir(), 'yingxu-export-'));
const dataDir = path.join(temporary, 'data');
const active = new Set();
const checkpoints = [];
const results = {checkpoints};
let origin;

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

function terminate(child, signal = 'SIGTERM') {
  if (child.exitCode !== null || child.signalCode !== null) return;
  try {process.kill(-child.pid, signal);} catch {child.kill(signal);}
}

function launch(command, args, options = {}) {
  const child = spawn(command, args, {cwd: root, env: {...process.env, ...options.env}, detached: true, stdio: ['ignore', 'pipe', 'pipe']});
  active.add(child);
  const stdout = [];
  const stderr = [];
  let stdoutSize = 0;
  let partial = '';
  const started = Date.now();
  child.stdout.on('data', chunk => {
    stdoutSize += chunk.length;
    if (stdoutSize <= 64 * 1024 * 1024) stdout.push(chunk);
    else terminate(child);
    if (options.onLine) {
      partial += chunk.toString();
      const lines = partial.split('\n');
      partial = lines.pop();
      for (const line of lines) if (line) options.onLine(line);
    }
  });
  child.stderr.on('data', chunk => stderr.push(chunk));
  const finished = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      terminate(child);
      const forced = setTimeout(() => terminate(child, 'SIGKILL'), 2000);
      forced.unref();
      reject(new Error(`验证超时：${path.basename(command)} ${args.join(' ')}`));
    }, options.timeout || 60000);
    child.once('error', error => {clearTimeout(timeout); active.delete(child); reject(error);});
    child.once('close', (code, signal) => {
      clearTimeout(timeout);
      active.delete(child);
      resolve({code, signal, stdout: Buffer.concat(stdout), stderr: Buffer.concat(stderr).toString(), elapsedSeconds: (Date.now() - started) / 1000});
    });
  });
  finished.catch(() => {});
  return {child, finished};
}

async function run(command, args, options = {}) {
  const result = await launch(command, args, options).finished;
  assert.equal(result.code, 0, `${path.basename(command)} 执行失败：${result.stderr}`);
  return result;
}

async function api(endpoint, body, method = body === undefined ? 'GET' : 'POST') {
  const response = await fetch(new URL(endpoint, origin), {method, headers: {'Content-Type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(5000)});
  const data = await response.json();
  assert.ok(response.ok, `接口 ${endpoint} 失败：${data.error || response.status}`);
  return data;
}

async function until(check, message, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const value = await check();
    if (value) return value;
    await delay(100);
  }
  throw new Error(message);
}

async function waitForJob(id, expected, timeout = 90000) {
  const seen = [];
  const job = await until(async () => {
    const value = await api(`/api/exports/${id}`);
    seen.push(value);
    if (['completed', 'failed', 'cancelled'].includes(value.status)) {
      assert.equal(value.status, expected, `导出状态错误：${value.message}`);
      return value;
    }
    return false;
  }, '导出任务未在验证时限内结束', timeout);
  return {job, seen};
}

function node(name, extra = {}) {
  return {id: randomUUID(), type: 'rect', name, x: 8, y: 8, width: 32, height: 32, rotation: 0, opacity: 1, color: '#ff0000', start: 0, end: 60, animation: 'none', locked: false, hidden: false, ...extra};
}

function fixture(extra = {}) {
  return {id: randomUUID(), name: '导出隔离验收', width: 256, height: 128, fps: 20, revision: 0, updatedAt: new Date().toISOString(), scenes: [{id: randomUUID(), name: '动画与图层边界', duration: 60, background: '#000000', notes: '', nodes: [
    node('淡入淡出', {x: 8, animation: 'fade'}),
    node('位移动画', {x: 56, color: '#00ff00', animation: 'slide'}),
    node('缩放动画', {x: 112, color: '#0000ff', width: 48, height: 48, animation: 'zoom'}),
    node('临时图层', {x: 176, y: 80, color: '#ffffff', start: 10, end: 20}),
    node('镜头内延迟图片', {type: 'image', x: 208, y: 8, width: 32, height: 32, src: '/uploads/fixture.svg', start: 15, end: 25})
  ]}, {id: randomUUID(), name: '静止镜头', duration: 40, background: '#22aa44', notes: '', nodes: []}], audio: [
    {id: randomUUID(), name: '裁剪淡入淡出', src: '/uploads/fixture.wav', start: 10, trimStart: 20, duration: 40, sourceDuration: 80, volume: 0.5, fadeIn: 5, fadeOut: 5},
    {id: randomUUID(), name: '第二段音轨', src: '/uploads/fixture.wav', start: 60, trimStart: 0, duration: 10, sourceDuration: 80, volume: 0.3, fadeIn: 0, fadeOut: 0}
  ], ...extra};
}

function waveform() {
  const rate = 16000;
  const samples = rate * 4;
  const buffer = Buffer.alloc(44 + samples * 2);
  buffer.write('RIFF', 0);
  buffer.writeUInt32LE(buffer.length - 8, 4);
  buffer.write('WAVE', 8);
  buffer.write('fmt ', 12);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(1, 22);
  buffer.writeUInt32LE(rate, 24);
  buffer.writeUInt32LE(rate * 2, 28);
  buffer.writeUInt16LE(2, 32);
  buffer.writeUInt16LE(16, 34);
  buffer.write('data', 36);
  buffer.writeUInt32LE(samples * 2, 40);
  for (let i = 0; i < samples; i++) buffer.writeInt16LE(Math.round(Math.sin(2 * Math.PI * (i < rate ? 330 : 880) * i / rate) * 22000), 44 + i * 2);
  return buffer;
}

async function verifyPixels(output) {
  const decoded = await run(process.env.FFMPEG_PATH || 'ffmpeg', ['-hide_banner', '-loglevel', 'error', '-i', output, '-an', '-pix_fmt', 'rgb24', '-f', 'rawvideo', 'pipe:1']);
  const pixels = decoded.stdout;
  const frameSize = 256 * 128 * 3;
  assert.equal(pixels.length / frameSize, 100, '输出帧数不符');
  const color = (frame, x, y) => Array.from(pixels.subarray(frame * frameSize + (y * 256 + x) * 3, frame * frameSize + (y * 256 + x) * 3 + 3));
  const near = (actual, expected) => assert.ok(actual.every((value, index) => Math.abs(value - expected[index]) < 28), `颜色不符：${actual}，预期 ${expected}`);
  near(color(0, 20, 20), [0, 0, 0]);
  near(color(12, 20, 20), [255, 0, 0]);
  assert.ok(color(3, 20, 20)[0] > color(1, 20, 20)[0] + 50, '淡入动画丢帧或被静止复用');
  assert.ok(color(59, 20, 20)[0] < color(55, 20, 20)[0] * 0.4, '淡出动画丢失');
  near(color(1, 68, 20), [0, 0, 0]);
  near(color(12, 68, 20), [0, 255, 0]);
  assert.ok(color(1, 114, 32)[2] < color(12, 114, 32)[2] * 0.3, '缩放动画没有改变边界');
  near(color(9, 188, 92), [0, 0, 0]);
  near(color(10, 188, 92), [255, 255, 255]);
  near(color(19, 188, 92), [255, 255, 255]);
  near(color(20, 188, 92), [0, 0, 0]);
  near(color(14, 220, 20), [0, 0, 0]);
  near(color(15, 220, 20), [255, 255, 0]);
  near(color(25, 220, 20), [0, 0, 0]);
  near(color(59, 240, 120), [0, 0, 0]);
  near(color(60, 240, 120), [34, 170, 68]);
  near(color(99, 240, 120), [34, 170, 68]);
  let staticDifference = 0;
  let staticMaximum = 0;
  for (let i = 0; i < frameSize; i++) {
    const difference = Math.abs(pixels[60 * frameSize + i] - pixels[80 * frameSize + i]);
    staticDifference += difference;
    staticMaximum = Math.max(staticMaximum, difference);
  }
  assert.ok(staticDifference / frameSize < 1.5 && staticMaximum < 9, `静止镜头内容改变：平均差 ${staticDifference / frameSize}，最大差 ${staticMaximum}`);
  checkpoints.push('100 帧完整有序，图层边界、图片加载及三种动画正确，静止镜头无变化');
}

async function verifyAudio(output) {
  const decoded = await run(process.env.FFMPEG_PATH || 'ffmpeg', ['-hide_banner', '-loglevel', 'error', '-i', output, '-vn', '-ac', '1', '-ar', '16000', '-f', 'f32le', 'pipe:1']);
  const sample = index => decoded.stdout.readFloatLE(index * 4);
  const rms = (start, duration = 0.06) => {
    let energy = 0;
    const count = Math.floor(duration * 16000);
    for (let i = 0; i < count; i++) energy += sample(Math.floor(start * 16000) + i) ** 2;
    return Math.sqrt(energy / count);
  };
  const tone = (start, frequency) => {
    let sine = 0;
    let cosine = 0;
    for (let i = 0; i < 1600; i++) {
      const value = sample(Math.floor(start * 16000) + i);
      sine += value * Math.sin(2 * Math.PI * frequency * i / 16000);
      cosine += value * Math.cos(2 * Math.PI * frequency * i / 16000);
    }
    return Math.hypot(sine, cosine);
  };
  assert.ok(rms(0.2) < 0.002, '音轨 start 延迟无效');
  assert.ok(rms(1.2) > 0.12, '输出配音为静音或音量不符');
  assert.ok(rms(0.5, 0.04) < rms(1.2) * 0.3, '淡入无效');
  assert.ok(rms(2.46, 0.03) < rms(1.2) * 0.3, '淡出无效');
  assert.ok(rms(2.7) < 0.002, '第一条音轨越过 duration');
  assert.ok(rms(3.1) > 0.09, '第二条音轨混合丢失');
  assert.ok(rms(3.8) < 0.002, '音轨结束后没有补静音');
  assert.ok(tone(1.2, 880) > tone(1.2, 330) * 20, 'trimStart 未裁去源音频开头');
  assert.ok(tone(3.1, 330) > tone(3.1, 880) * 20, '第二条音轨 trimStart 有误');
  checkpoints.push('两条音轨裁剪、延迟、混合、淡入淡出及末尾补静音正确');
}

async function absent(filename) {
  try {await access(filename); return false;} catch (error) {if (error.code === 'ENOENT') return true; throw error;}
}

async function verifyClean(id) {
  await until(async () => await absent(path.join(dataDir, 'exports', `${id}.mp4`)) && await absent(path.join(dataDir, 'exports', `${id}.mp4.silent.mp4`)) && await absent(path.join(dataDir, 'export-jobs', `${id}.project.json`)), '失败或取消的导出残留临时文件');
}

try {
  await access(path.join(root, 'dist', 'render.html'));
  await mkdir(path.join(dataDir, 'uploads'), {recursive: true});
  await writeFile(path.join(dataDir, 'uploads', 'fixture.wav'), waveform());
  await writeFile(path.join(dataDir, 'uploads', 'fixture.svg'), '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="#ffff00"/></svg>');
  const portProbe = createServer();
  await new Promise(resolve => portProbe.listen(0, '127.0.0.1', resolve));
  const port = portProbe.address().port;
  await new Promise(resolve => portProbe.close(resolve));
  origin = `http://127.0.0.1:${port}`;
  const binary = process.env.TEST_VIDEO_BIN || path.join(temporary, 'yingxu');
  if (!process.env.TEST_VIDEO_BIN) {
    let go = process.env.GO_PATH || 'go';
    if (go === 'go') {
      const fallback = path.join(process.env.HOME || '', '.local', 'go', 'bin', 'go');
      if (!await absent(fallback)) go = fallback;
    }
    await run(go, ['build', '-o', binary, './cmd/server']);
  }
  const server = launch(binary, ['-addr', `127.0.0.1:${port}`, '-data', dataDir, '-dist', path.join(root, 'dist')], {timeout: 300000, env: {VIDEO_ROOT: root, RENDER_ORIGIN: origin}});
  await until(async () => {
    try {return (await api('/api/health')).ok;} catch {return false;}
  }, '隔离服务未启动');
  const saved = await api('/api/projects', fixture());
  const started = Date.now();
  const created = await api('/api/exports', {project: saved});
  const {job, seen} = await waitForJob(created.id, 'completed');
  const output = path.join(dataDir, 'exports', `${created.id}.mp4`);
  const probe = JSON.parse((await run(process.env.FFPROBE_PATH || 'ffprobe', ['-v', 'error', '-count_frames', '-show_streams', '-show_format', '-of', 'json', output])).stdout.toString());
  const video = probe.streams.find(stream => stream.codec_type === 'video');
  const audio = probe.streams.find(stream => stream.codec_type === 'audio');
  assert.equal(Number(video.nb_read_frames), 100, '容器帧数错误');
  assert.equal(video.width, 256);
  assert.equal(video.height, 128);
  assert.equal(video.avg_frame_rate, '20/1');
  assert.ok(Math.abs(Number(probe.format.duration) - 5) < 0.06, '输出时长错误');
  assert.equal(audio?.codec_name, 'aac', '音轨没有写入容器');
  assert.equal(job.progress, 1, '完成后进度没有达到 100%');
  assert.ok(seen.every((value, index) => !index || value.progress >= seen[index - 1].progress), '导出进度倒退');
  assert.equal(job.renderedFrames, 100, '导出帧计数错误');
  assert.equal(job.totalFrames, 100, '工程总帧数错误');
  results.animatedExportSeconds = (Date.now() - started) / 1000;
  await verifyPixels(output);
  await verifyAudio(output);
  checkpoints.push('异步导出 API 完成，容器帧数、分辨率、帧率、音轨和时长正确');

  const still = fixture({id: randomUUID(), name: '静止帧复用验证', fps: 30, scenes: [{id: randomUUID(), name: '静止内容', duration: 90, background: '#22aa44', notes: '', nodes: []}], audio: []});
  const stillPath = path.join(temporary, 'still.json');
  const stillOutput = path.join(temporary, 'still.mp4');
  await writeFile(stillPath, JSON.stringify(still));
  const progress = [];
  const stillResult = await run(process.execPath, [path.join(root, 'scripts', 'render.mjs'), stillPath, stillOutput, origin, dataDir], {onLine: line => {
    try {progress.push(JSON.parse(line));} catch {}
  }});
  const final = progress.at(-1);
  assert.equal(final.stage, 'completed');
  assert.equal(final.renderedFrames, 90);
  assert.ok(final.capturedFrames >= 1 && final.capturedFrames <= 2, `静止镜头仍然逐帧截图：${final.capturedFrames}`);
  assert.ok(progress.some(value => value.stage === 'rendering'), '渲染阶段进度缺失');
  results.staticExportSeconds = stillResult.elapsedSeconds;
  results.staticCapturedFrames = final.capturedFrames;
  checkpoints.push('90 个静止帧只捕获 1 至 2 次，所有视频帧仍被编码');

  const broken = fixture({id: randomUUID(), name: '图片错误清理验证', scenes: [{id: randomUUID(), name: '缺失图片', duration: 10, background: '#000000', notes: '', nodes: [node('缺失素材', {type: 'image', src: '/uploads/missing.png', end: 10})]}], audio: []});
  const brokenJob = await api('/api/exports', {project: broken});
  const brokenResult = await waitForJob(brokenJob.id, 'failed', 30000);
  assert.match(brokenResult.job.message, /图片|素材/);
  await verifyClean(brokenJob.id);
  checkpoints.push('缺失图片迅速失败并清理导出文件');

  const long = fixture({id: randomUUID(), name: '导出取消验证', fps: 30, scenes: [{id: randomUUID(), name: '长动画', duration: 3000, background: '#000000', notes: '', nodes: [node('动画', {end: 3000, animation: 'slide'})]}], audio: []});
  const cancelJob = await api('/api/exports', {project: long});
  await until(async () => {
    const value = await api(`/api/exports/${cancelJob.id}`);
    return value.status === 'running' && value.renderedFrames > 0;
  }, '取消验证任务未开始渲染', 30000);
  await api(`/api/exports/${cancelJob.id}/cancel`, {});
  const cancelled = await waitForJob(cancelJob.id, 'cancelled', 15000);
  assert.equal(cancelled.job.url, undefined, '取消任务仍提供下载');
  await verifyClean(cancelJob.id);
  checkpoints.push('运行中导出可取消，状态收敛且临时文件清理');

  const failureOutput = path.join(temporary, 'encoder-failure.mp4');
  const encoderFailure = await launch(process.execPath, [path.join(root, 'scripts', 'render.mjs'), stillPath, failureOutput, origin, dataDir], {timeout: 15000, env: {FFMPEG_PATH: process.execPath}}).finished;
  assert.notEqual(encoderFailure.code, 0, '编码器失败没有反馈');
  assert.ok(await absent(failureOutput) && await absent(`${failureOutput}.silent.mp4`), '编码失败残留文件');
  checkpoints.push('编码器提前退出时立即失败，无管道永久等待及残留');
  terminate(server.child);
  await server.finished;
  await mkdir(path.join(root, 'test-results'), {recursive: true});
  await writeFile(path.join(root, 'test-results', 'export-verification.json'), JSON.stringify(results, null, 2));
  process.stdout.write(`${JSON.stringify(results, null, 2)}\n`);
} finally {
  if (origin) {
    try {
      const jobs = await api('/api/exports');
      for (const job of jobs) if (job.status === 'running' || job.status === 'queued') await api(`/api/exports/${job.id}/cancel`, {});
    } catch {}
  }
  for (const child of active) terminate(child);
  await delay(300);
  for (const child of active) terminate(child, 'SIGKILL');
  await rm(temporary, {recursive: true, force: true});
}
