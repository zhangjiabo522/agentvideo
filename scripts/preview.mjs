import {readFile} from 'node:fs/promises';
import {chromium} from 'playwright';
import {chromiumPath} from './chromium.mjs';

const [projectPath, outputPath, origin, frameValue] = process.argv.slice(2);
let browser;

async function preview() {
  if (!projectPath || !outputPath || !origin || frameValue === undefined) throw new Error('截图参数不完整');
  const target = new URL(origin);
  if (target.hostname !== '127.0.0.1' && target.hostname !== 'localhost' && target.hostname !== '[::1]') throw new Error('截图仅允许本机渲染地址');
  const project = JSON.parse(await readFile(projectPath, 'utf8'));
  const frame = Number(frameValue);
  const total = project.scenes.reduce((sum, scene) => sum + scene.duration, 0);
  if (!Number.isInteger(frame) || frame < 0 || frame >= total) throw new Error('截图帧超出工程范围');
  browser = await chromium.launch({headless: true, executablePath: await chromiumPath(), args: ['--no-sandbox', '--disable-dev-shm-usage', '--font-render-hinting=none']});
  const page = await browser.newPage({viewport: {width: project.width, height: project.height}, deviceScaleFactor: 1, reducedMotion: 'reduce'});
  page.setDefaultTimeout(20000);
  await page.goto(new URL('/render.html', target).href, {waitUntil: 'networkidle'});
  await page.waitForFunction(() => window.__rendererReady === true);
  await page.evaluate(value => window.__setProject(value), project);
  await page.evaluate(value => window.__renderFrame(value), frame);
  await page.screenshot({path: outputPath, type: 'png', animations: 'disabled', scale: 'css'});
}

process.once('SIGTERM', () => {void browser?.close();});
process.once('SIGINT', () => {void browser?.close();});

try {
  await preview();
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
} finally {
  await browser?.close().catch(() => {});
}
