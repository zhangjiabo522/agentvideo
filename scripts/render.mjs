import {readFile, access, rename, rm} from 'node:fs/promises';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {chromium} from 'playwright';
import {chromiumPath} from './chromium.mjs';

const [projectPath, outputPath, origin, dataDir] = process.argv.slice(2);
const children = new Set();
let browser;
let cancelled = false;
let currentProgress = {progress: 0, message: '正在准备渲染环境', stage: 'preparing', renderedFrames: 0, totalFrames: 0, capturedFrames: 0};
let lastReportedAt = 0;
const silentPath = outputPath ? `${outputPath}.silent.mp4` : '';
const report = (progress, message, stage, details = {}) => {
  const changedStage = stage !== currentProgress.stage;
  currentProgress = {...currentProgress, progress, message, stage, ...details};
  if (!changedStage && Date.now() - lastReportedAt < 500 && progress < 1) return;
  lastReportedAt = Date.now();
  process.stdout.write(`${JSON.stringify(currentProgress)}\n`);
};
const heartbeat = setInterval(() => {
  lastReportedAt = Date.now();
  process.stdout.write(`${JSON.stringify(currentProgress)}\n`);
}, 2000);
heartbeat.unref();

async function deadline(operation, message, timeout = 30000) {
  let timer;
  try {
    return await Promise.race([operation, new Promise((_, reject) => {timer = setTimeout(() => reject(new Error(message)), timeout);})]);
  } finally {clearTimeout(timer);}
}

async function writeFrame(encoder, image) {
  if (encoder.child.exitCode !== null || encoder.child.stdin.destroyed) throw new Error('视频编码器提前停止，请重新导出');
  if (encoder.child.stdin.write(image)) return;
  let cleanup;
  const drained = new Promise((resolve, reject) => {
    const stream = encoder.child.stdin;
    const done = () => {cleanup(); resolve();};
    const failed = () => {cleanup(); reject(new Error('视频编码器停止接收画面，请重新导出'));};
    cleanup = () => {
      stream.removeListener('drain', done);
      stream.removeListener('error', failed);
      stream.removeListener('close', failed);
    };
    stream.once('drain', done);
    stream.once('error', failed);
    stream.once('close', failed);
  });
  try {await deadline(drained, '视频编码等待超时，请重新导出');} finally {cleanup();}
}

function runFFmpeg(args, stream = false) {
  const child = spawn(process.env.FFMPEG_PATH || 'ffmpeg', args, {stdio: [stream ? 'pipe' : 'ignore', 'ignore', 'pipe']});
  children.add(child);
  let errors = '';
  child.stderr.on('data', chunk => {errors = `${errors}${chunk}`.slice(-8000);});
  const finished = new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', code => {
      children.delete(child);
      if (code === 0) resolve();
      else reject(new Error(cancelled ? '导出已取消' : `视频编码失败：${errors || `退出状态 ${code}`}`));
    });
  });
  finished.catch(() => {});
  if (stream) child.stdin.on('error', () => {});
  return {child, finished};
}

async function audioFile(src) {
  const url = new URL(src, origin);
  if (url.origin !== new URL(origin).origin || !url.pathname.startsWith('/uploads/')) throw new Error('音频需先上传到素材库');
  const filename = decodeURIComponent(url.pathname.slice('/uploads/'.length));
  if (!filename || path.basename(filename) !== filename || filename.includes('..')) throw new Error('音频路径无效');
  const resolved = path.join(dataDir, 'uploads', filename);
  await access(resolved);
  return resolved;
}

async function exportVideo() {
  if (!projectPath || !outputPath || !origin || !dataDir) throw new Error('渲染参数不完整');
  const project = JSON.parse(await readFile(projectPath, 'utf8'));
  const count = project.scenes.reduce((total, scene) => total + scene.duration, 0);
  if (!count || count / project.fps > 120 || project.scenes.length > 20 || project.scenes.reduce((total, scene) => total + scene.nodes.length, 0) > 300) throw new Error('工程超出导出范围');
  if (!Number.isInteger(project.width) || !Number.isInteger(project.height) || project.width < 64 || project.height < 64 || project.width > 1920 || project.height > 1920 || project.width % 2 || project.height % 2) throw new Error('画布需为 64 到 1920 像素的偶数尺寸');
  if (!Number.isInteger(project.fps) || project.fps < 1 || project.fps > 60) throw new Error('帧率需为 1 到 60');
  const tracks = await Promise.all((project.audio || []).filter(track => track.duration > 0 && track.volume > 0 && track.start < count).map(async track => ({...track, file: await audioFile(track.src)})));
  if (tracks.length > 12) throw new Error('最多支持 12 条音轨');
  report(0.01, '正在准备渲染环境', 'preparing', {totalFrames: count});
  browser = await chromium.launch({headless: true, executablePath: await chromiumPath(), timeout: 20000, args: ['--no-sandbox', '--disable-dev-shm-usage', '--font-render-hinting=none']});
  const page = await browser.newPage({viewport: {width: project.width, height: project.height}, deviceScaleFactor: 1, reducedMotion: 'reduce'});
  page.setDefaultTimeout(20000);
  await page.goto(new URL('/render.html', origin).href, {waitUntil: 'domcontentloaded', timeout: 20000});
  await page.waitForFunction(() => window.__rendererReady === true);
  report(0.02, '正在加载图片和字体', 'preparing');
  await deadline(page.evaluate(project => window.__setProject(project), project), '素材加载超时，请检查图片后重试');
  const capture = await page.context().newCDPSession(page);
  const encoder = runFFmpeg(['-hide_banner', '-loglevel', 'error', '-y', '-f', 'image2pipe', '-vcodec', 'png', '-framerate', String(project.fps), '-i', 'pipe:0', '-an', '-c:v', 'libx264', '-preset', 'veryfast', '-crf', '20', '-pix_fmt', 'yuv420p', '-threads', '2', '-movflags', '+faststart', silentPath], true);
  let previousImage;
  let capturedFrames = 0;
  for (let frame = 0; frame < count; frame++) {
    if (cancelled) throw new Error('导出已取消');
    const changed = await deadline(page.evaluate(frame => window.__renderFrame(frame), frame), '画面渲染超时，请检查素材后重试');
    if (changed !== false || !previousImage) {
      const shot = await deadline(capture.send('Page.captureScreenshot', {format: 'png', optimizeForSpeed: true, captureBeyondViewport: false}), '画面截图超时，请重新导出');
      previousImage = Buffer.from(shot.data, 'base64');
      capturedFrames++;
    }
    await writeFrame(encoder, previousImage);
    report(0.04 + (frame + 1) / count * 0.89, `正在渲染 ${frame + 1} / ${count} 帧`, 'rendering', {renderedFrames: frame + 1, capturedFrames});
  }
  report(0.94, '正在完成视频编码', 'encoding');
  encoder.child.stdin.end();
  await deadline(encoder.finished, '视频编码超时，请重新导出', 60000);
  await capture.detach();
  await browser.close();
  browser = undefined;
  if (tracks.length) {
    report(0.95, '正在混合音轨', 'audio');
    const args = ['-hide_banner', '-loglevel', 'error', '-y', '-i', silentPath];
    tracks.forEach(track => args.push('-ss', String(track.trimStart / project.fps), '-t', String(track.duration / project.fps), '-i', track.file));
    const filters = tracks.map((track, index) => {
      const duration = track.duration / project.fps;
      const effects = [`atrim=duration=${duration}`, 'asetpts=PTS-STARTPTS', `volume=${track.volume}`];
      if (track.fadeIn > 0) effects.push(`afade=t=in:st=0:d=${Math.min(track.duration, track.fadeIn) / project.fps}`);
      if (track.fadeOut > 0) effects.push(`afade=t=out:st=${Math.max(0, track.duration - track.fadeOut) / project.fps}:d=${Math.min(track.duration, track.fadeOut) / project.fps}`);
      effects.push(`adelay=${Math.round(track.start / project.fps * 1000)}:all=1`);
      return `[${index + 1}:a]${effects.join(',')}[a${index}]`;
    });
    filters.push(`${tracks.map((_, index) => `[a${index}]`).join('')}amix=inputs=${tracks.length}:duration=longest:normalize=0,apad[audio]`);
    args.push('-filter_complex', filters.join(';'), '-map', '0:v:0', '-map', '[audio]', '-c:v', 'copy', '-c:a', 'aac', '-b:a', '192k', '-t', String(count / project.fps), '-movflags', '+faststart', outputPath);
    await deadline(runFFmpeg(args).finished, '音轨合成超时，请重新导出', 60000);
    await rm(silentPath, {force: true});
  } else await rename(silentPath, outputPath);
  report(1, '视频导出完成', 'completed');
}

async function stop() {
  cancelled = true;
  for (const child of children) child.kill('SIGTERM');
  if (browser) await deadline(browser.close(), '渲染器关闭超时', 5000).catch(() => {});
}

process.once('SIGTERM', stop);
process.once('SIGINT', stop);

try {
  await exportVideo();
} catch (error) {
  const interrupted = cancelled;
  await stop();
  if (silentPath) await rm(silentPath, {force: true});
  if (outputPath) await rm(outputPath, {force: true});
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = interrupted ? 2 : 1;
} finally {clearInterval(heartbeat);}
