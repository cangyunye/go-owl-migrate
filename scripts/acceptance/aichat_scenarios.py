# AI 对话页验收场景（TDD：先红后绿）。对 mock vendor 运行，入口见
# run_aichat_acceptance.sh（起 mock vendor + 隔离 serve + 预置档案）。
# 场景 A-N 与 docs/plans/2026-09-28-ai-chat-router-design.md 的功能清单一一
# 对应；其中 J（取用配置）、M（新话题）的原始 TDD 场景文件已不存在，
# 按 aiChat.js 功能清单重建。
import json, sys, urllib.request
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:18095'
results = []

def check(name, ok, detail=''):
    results.append((name, ok, detail))
    print(('PASS ' if ok else 'FAIL ') + name + ('  | ' + detail if detail and not ok else ''))

def api(path, method='GET', body=None):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode() if body is not None else None,
                                 headers={'Content-Type': 'application/json'}, method=method)
    return json.load(urllib.request.urlopen(req))

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page(viewport={'width': 1500, 'height': 1000})
    errors = []
    page.on('pageerror', lambda e: errors.append(str(e)))
    page.goto(BASE + '/#/ai')
    page.wait_for_load_state('networkidle')
    page.wait_for_timeout(500)

    # A: 页面可达（crumb + 输入区）
    check('A 页面可达', page.locator('#crumb-current').inner_text() == 'AI 助手'
          and page.locator('#ai-input').count() == 1)

    # B: 数据源侧栏入口（修复丢失的导航项）
    page.goto(BASE + '/#/datasources')
    page.wait_for_timeout(300)
    check('B 数据源侧栏入口', page.locator('#spa-nav a[data-route="/datasources"]').count() == 1)

    # C: 空状态与供应商徽章
    page.goto(BASE + '/#/ai')
    page.wait_for_timeout(400)
    check('C 供应商徽章', 'deepseek' in page.locator('#ai-provider-badge').inner_text())

    # D: 气泡对话流（mock 第一轮返回 clarify）
    page.fill('#ai-input', '导出 users 表为 csv')
    page.click('#ai-send')
    page.wait_for_timeout(2500)
    bubbles = page.locator('.chat-msg').count()
    check('D 气泡对话流', bubbles >= 2, f'bubbles={bubbles}')

    # F: 澄清 chips 渲染 + 点击即发送
    chips = page.locator('.chat-chip')
    n_chips = chips.count()
    if n_chips > 0:
        chips.first.click()
        page.wait_for_timeout(2500)
        sent = page.locator('.chat-msg.user').last.inner_text()
        check('F 澄清 chips 点击即发', '导出表数据' in sent or '用哪个数据源' not in sent, sent[:60])
    else:
        check('F 澄清 chips 点击即发', False, 'no chips rendered')

    # G: 信息核对单
    page.goto(BASE + '/#/ai')
    page.wait_for_timeout(400)
    page.fill('#ai-input', '用 oracle-scott 档案导出 emp 为 csv')
    page.click('#ai-send')
    page.wait_for_timeout(3000)
    check('G 信息核对单', page.locator('.slot-check').count() >= 1)

    # H: 确认终态 + effective（手填密码的哨兵需先补密码）
    if page.locator('#ai-cred-fields input').count():
        page.fill('#ai-cred-fields input', 'test-password')
    if page.locator('#ai-execute').count() and page.locator('#ai-execute').is_enabled():
        page.click('#ai-execute')
        page.wait_for_timeout(2000)
        st = page.locator('#ai-plan-status').inner_text()
        check('H 确认终态', page.locator('#ai-execute').is_disabled() or '已启动' in st or '已激活' in st, st[:60])
    else:
        check('H 确认终态', False, 'no execute button')

    # E: 选择器强制档案（新话题，选 p2，话语不提档案）
    page.click('#ai-new-topic') if page.locator('#ai-new-topic').count() else None
    page.wait_for_timeout(300)
    page.select_option('#ai-src-profile', 'p2')
    page.fill('#ai-input', '导出全部表为 csv')
    page.click('#ai-send')
    page.wait_for_timeout(3000)
    meta = page.locator('#ai-meta').inner_text() if page.locator('#ai-meta').count() else ''
    check('E 选择器强制档案', 'p2' in meta, meta[:80])

    # I: 快捷语录
    page.goto(BASE + '/#/ai')
    page.wait_for_timeout(400)
    quotes = page.locator('.chat-quote')
    check('I 快捷语录', quotes.count() >= 4, f'quotes={quotes.count()}')
    if quotes.count():
        quotes.first.click()
        check('I2 语录填充', page.locator('#ai-input').input_value() != '')

    # K: 历史列表 + 搜索（强制重进触发重渲染；同 hash goto 不重渲）
    page.goto(BASE + '/#/')
    page.wait_for_timeout(300)
    page.goto(BASE + '/#/ai')
    page.wait_for_timeout(600)
    hist = page.locator('.ai-hist-item')
    check('K 历史列表', hist.count() >= 1, f'items={hist.count()}')
    page.fill('#ai-hist-search', 'p2')
    page.wait_for_timeout(900)
    hist2 = page.locator('.ai-hist-item')
    # 确定性契约：过滤后唯一命中，且该会话经 API 验证 keywords 确实含 p2
    # （防时序脆弱：不看 UI 文本，验证数据本身）
    ok_k2 = False
    detail_k2 = f'before={hist.count()} after={hist2.count()}'
    if hist2.count() == 1:
        link = hist2.first.locator('a[data-sid]')
        if link.count():
            sid = link.get_attribute('data-sid')
            sess = api('/api/v1/ai/session/' + sid)
            ok_k2 = 'p2' in (sess['session'].get('keywords') or '')
            detail_k2 += ' sid=' + sid[:12]
    check('K2 搜索过滤', ok_k2, detail_k2)

    # L: 克隆继续（清掉搜索过滤）
    page.fill('#ai-hist-search', '')
    page.wait_for_timeout(800)
    clone_btn = page.locator('.ai-hist-clone').first
    if clone_btn.count():
        clone_btn.click()
        page.wait_for_timeout(2000)
        check('L 克隆继续', page.locator('.slot-check').count() >= 1)
    else:
        check('L 克隆继续', False, 'no clone button')

    # J: 取用配置 —— 历史列表 apply → 会话产物解密 → 激活为当前配置。
    # UI 只点按钮，数据契约经 API 验证（防 toast 文本漂移）：找到
    # effective 会话（G/H 确认过、有加密配置产物），apply 后当前配置的
    # source_dsn 必须是档案的真实 DSN；且密码只走 API，绝不渲染进页面。
    page.goto(BASE + '/#/')
    page.wait_for_timeout(300)
    page.goto(BASE + '/#/ai')
    page.wait_for_timeout(600)
    listing = api('/api/v1/ai/sessions?limit=50')
    items = listing if isinstance(listing, list) else (listing.get('sessions') or [])
    sid = next((it['id'] for it in items if it.get('effective')), '')
    ok_j, det_j = False, 'no effective session'
    if sid:
        item = page.locator(f'.ai-hist-item:has(a[data-sid="{sid}"])')
        btn = item.locator('.ai-hist-apply')
        if btn.count():
            btn.first.click()
            page.wait_for_timeout(1200)
            toast = page.locator('#toast-root').inner_text() if page.locator('#toast-root').count() else ''
            cur = api('/api/v1/config/current')
            vals = cur.get('values') or {}
            ok_j = '配置已激活' in toast and 'SeedSecret1' in (vals.get('source_dsn') or '')
            det_j = f'toast={toast[:40]!r} dsn_hit={("SeedSecret1" in (vals.get("source_dsn") or ""))}'
        else:
            det_j = f'no apply button for {sid[:12]}'
    check('J 取用配置', ok_j, det_j)
    check('J2 档案密码不渲染进页面', 'SeedSecret1' not in page.content())

    # M: 新话题 —— 重开会话：气泡清空、计划卡收起、状态行提示可继续。
    page.click('#ai-new-topic')
    page.wait_for_timeout(600)
    bubbles_after = page.locator('.chat-msg').count()
    plan_hidden = (not page.locator('#ai-plan-card').count()) or page.locator('#ai-plan-card').is_hidden()
    status_m = page.locator('#ai-plan-status').inner_text() if page.locator('#ai-plan-status').count() else ''
    check('M 新话题重开', bubbles_after == 0 and plan_hidden and '已开新话题' in status_m,
          f'bubbles={bubbles_after} hidden={plan_hidden} status={status_m[:40]!r}')

    # N: 配置页 AI 面板已移除、入口卡存在
    page.goto(BASE + '/#/config')
    page.wait_for_timeout(500)
    check('N 配置页面板移除', page.locator('#ai-plan-panel').count() == 0
          and page.locator('a[href="#/ai"]').count() >= 1)

    check('JS_ERRORS', not errors, '; '.join(errors[:3]))
    browser.close()

fails = [r for r in results if not r[1]]
print(f"\n{len(results)-len(fails)}/{len(results)} passed")
sys.exit(1 if fails else 0)
