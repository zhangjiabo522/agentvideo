import {readFile, access, rename, rm} from 'node:fs/promises';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {chromium} from 'playwright';

const [projectPath, outputPath, origin, dataDir] = process.argv.slice(2);
const children = new Set();
let browser;
let cancelled = false;
let lastProgress = -1;
const silentPath = outputPath ? `${outputPath}.silent.mp4` : '';
const report = (progress, message) => {
  const percentage = Math.floor(progress * 100);
  if (percentage === lastProgress && progress < 1) return;
  lastProgress = percentage;
  process.stdout.write(`${JSON.stringify({progress, message})}\n`);
};

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
  const executablePath = process.env.CHROME_PATH || '/home/icy/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome';
  let installedPath;
  try {await access(executablePath); installedPath = executablePath;} catch {installedPath = undefined;}
  report(0.01, '正在准备渲染环境');
  browser = await chromium.launch({headless: true, executablePath: installedPath, args: ['--no-sandbox', '--disable-dev-shm-usage', '--font-render-hinting=none']});
  const page = await browser.newPage({viewport: {width: project.width, height: project.height}, deviceScaleFactor: 1, reducedMotion: 'reduce'});
  page.setDefaultTimeout(20000);
  await page.goto(new URL('/render.html', origin).href, {waitUntil: 'networkidle'});
  await page.waitForFunction(() => window.__rendererReady === true);
  await page.evaluate(project => window.__setProject(project), project);
  const encoder = runFFmpeg(['-hide_banner', '-loglevel', 'error', '-y', '-f', 'image2pipe', '-vcodec', 'png', '-framerate', String(project.fps), '-i', 'pipe:0', '-an', '-c:v', 'libx264', '-preset', 'veryfast', '-crf', '20', '-pix_fmt', 'yuv420p', '-threads', '2', '-movflags', '+faststart', silentPath], true);
  for (let frame = 0; frame < count; frame++) {
    if (cancelled) throw new Error('导出已取消');
    await page.evaluate(frame => window.__renderFrame(frame), frame);
    const png = await page.screenshot({type: 'png', animations: 'disabled', scale: 'css'});
    if (!encoder.child.stdin.write(png)) await once(encoder.child.stdin, 'drain');
    report(0.04 + (frame + 1) / count * 0.89, `正在渲染 ${frame + 1} / ${count} 帧`);
  }
  encoder.child.stdin.end();
  await encoder.finished;
  await browser.close();
  browser = undefined;
  if (tracks.length) {
    report(0.95, '正在混合音轨');
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
    await runFFmpeg(args).finished;
    await rm(silentPath, {force: true});
  } else await rename(silentPath, outputPath);
  report(1, '视频导出完成');
}

async function stop() {
  cancelled = true;
  for (const child of children) child.kill('SIGTERM');
  if (browser) await browser.close().catch(() => {});
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
}
