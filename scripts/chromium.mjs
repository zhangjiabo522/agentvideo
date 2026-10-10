import {access, readdir} from 'node:fs/promises';
import path from 'node:path';
import {chromium} from 'playwright';

export async function chromiumPath() {
  if (process.env.CHROME_PATH) {
    try {await access(process.env.CHROME_PATH); return process.env.CHROME_PATH;}
    catch {throw new Error('指定的 Chromium 不存在，请检查 CHROME_PATH');}
  }
  const expected = chromium.executablePath();
  try {await access(expected); return expected;} catch {}
  const location = expected.match(/^(.*[\\/])chromium-\d+([\\/].+)$/);
  if (location) {
    const versions = await readdir(location[1]).catch(() => []);
    versions.sort((a, b) => Number(b.split('-').at(-1)) - Number(a.split('-').at(-1)));
    for (const version of versions.filter(value => /^chromium-\d+$/.test(value))) {
      const candidate = path.join(location[1], version) + location[2];
      try {await access(candidate); return candidate;} catch {}
    }
  }
  throw new Error('未找到 Chromium，请运行 npx playwright install chromium 后重试');
}
