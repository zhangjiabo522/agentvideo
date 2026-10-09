import { spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";

const candidates = [
  process.env.GO_PATH,
  "go",
  path.join(homedir(), ".local/go/bin/go"),
  path.join(homedir(), ".local/go1.27.2/bin/go"),
  path.join(homedir(), ".local/go1.24/bin/go"),
].filter(Boolean);
const go = candidates.find(
  (candidate) =>
    (candidate === "go" || existsSync(candidate)) &&
    spawnSync(candidate, ["version"], { stdio: "ignore" }).status === 0,
);
if (!go) {
  console.error(
    "未找到 Go，请安装 Go 1.22 或更新版本，或通过 GO_PATH 指定可执行文件。",
  );
  process.exit(1);
}
const build = spawnSync(
  process.platform === "win32" ? "npm.cmd" : "npm",
  ["run", "build"],
  { stdio: "inherit" },
);
if (build.status !== 0) process.exit(build.status || 1);
const server = spawn(go, ["run", "./cmd/server"], { stdio: "inherit" });
server.on("error", (error) => {
  console.error("服务启动失败：" + error.message);
  process.exitCode = 1;
});
server.on("exit", (code) => {
  process.exitCode = code || 0;
});
process.on("SIGINT", () => server.kill("SIGINT"));
process.on("SIGTERM", () => server.kill("SIGTERM"));
