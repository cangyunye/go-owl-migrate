# ADR — SPA Cutover（移除 SSR，前端收敛为单一 SPA）

Status: accepted (2026-08-27). Supersedes the dual-track SSR+SPA coexistence.

## Context

`owl-migrate serve` 的前端经历了三阶段：
- **初始**：多页 + Go `html/template` 服务端渲染（SSR）+ 原生 IIFE JS，全部 `//go:embed` 进单个二进制（ADR-005/007）。
- **Phase 1/2**：新增 SPA 壳与 ES-module 视图（`/ui`），逐步把 9 个 SSR 页面搬进 `views/*.js`，形成 **SSR + SPA 双轨**。
- **Phase 3（本次）**：删除 SSR 页面/模板/专用 JS，把 SPA 挂到 `/`，双轨收敛为单轨。

双轨期的真实痛点：
1. **维护双份**：导航、模板、JS 各一套，改动要同步两处（数据源菜单曾只加进 SPA、SSR 侧遗漏）。
2. **交互割裂**：实时迁移进度（WebSocket）/ 配置实时 YAML 预览 / 多步表单，在整页刷新的 SSR 下体验差。
3. **鉴权断层**：带 token 部署时，SSR 页无 token 弹窗，`/api` 401 后 `config.js` 无兜底 → 内容静默空白（本次已修）。

## Decision

**前端收敛为单一 SPA（手写 ES modules），移除 SSR**：
- 服务端只负责静态资源 + `/`（与 `/ui`）返回 SPA 壳 + `/api/v1/*` + `/docs`。
- 每页 = 一个 `views/*.js`（`export function render(root, params)`）+ hash 路由；共享 `window` 内核（`api/toast/jobUI/highlight*`）+ `util.js`。
- **保留** `web/static/js/app.js`（被 SPA 壳加载的共享内核，同时充当 SSR 时代的全局入口）。
- 无框架、无 Node 构建、无 CDN —— 延续「单二进制、零外部运行时」的初衷。

## Consequences

正向：
- 一套前端，导航/模板/JS 不再双维护。
- 实时进度、实时 YAML 预览、表单交互无整页刷新。
- 鉴权（token 弹窗）在壳层统一处理，一处生效。
- XSS 可走 DOM/textContent + escapeHtml，较 SSR 模板插值更可控。

代价（对本产品可接受）：
- 首屏需加载 JS（内部工具、现代浏览器，可接受）。
- 无 SSR 的页面级缓存 / “无 JS 可用” / 真路径深链接（改为 `#/config` hash，微小影响）。
- SSR 的 SEO 语义价值对本工具的 DBA 受众无意义。

## 关联

- 计划：`docs/plans/2026-08-27-phase3-spa-cutover.md`
- 目录指南：`docs/development.md`，CLI/serve 覆盖对照：`docs/serve-cli-coverage-report.md`
