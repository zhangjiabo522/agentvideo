import {mkdir, readdir, access} from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const cache = path.join(root, '.cache', 'tts');
const target = path.join(cache, 'espeak-ng');

function run(command, args, cwd = root) {
  return new Promise((resolve, reject) => {
    const process = spawn(command, args, {cwd, stdio: 'inherit'});
    process.once('error', reject);
    process.once('exit', code => code === 0 ? resolve() : reject(new Error(`${command} 执行失败，状态 ${code}`)));
  });
}

try {
  if (process.platform !== 'linux') throw new Error('该脚本适用于 Linux Debian / Ubuntu；其他系统请安装 espeak-ng 并设置 LOCAL_TTS_PATH');
  await mkdir(cache, {recursive: true});
  await mkdir(target, {recursive: true});
  try {
    await access('/usr/lib/x86_64-linux-gnu/libespeak-ng.so.1');
    await access('/usr/lib/x86_64-linux-gnu/espeak-ng-data/cmn_dict');
  } catch {
    throw new Error('本机需要 libespeak-ng1 和 espeak-ng-data。请安装这两个系统包后重试');
  }
  let binary = path.join(target, 'usr', 'bin', 'espeak-ng');
  try {await access(binary);} catch {
    await run('apt', ['download', 'espeak-ng'], cache);
    const packages = (await readdir(cache)).filter(name => name.startsWith('espeak-ng_') && name.endsWith('.deb'));
    if (!packages.length) throw new Error('未下载到 espeak-ng 软件包');
    await run('dpkg-deb', ['-x', path.join(cache, packages[0]), target]);
  }
  await run(binary, ['--voices=cmn']);
  process.stdout.write('本地中文配音已就绪，使用 espeak-ng 机械音。\n');
} catch (error) {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
}
