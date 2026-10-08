#!/usr/bin/env python3
# OpenAI 兼容 mock vendor，供 run_aichat_acceptance.sh 调起（端口见该脚本）。
#
# 按请求里的 system prompt 区分 /ai/plan 的两段调用：
#   - 含 "SlotRequest" → 槽位提取段：按用户话语返回确定性槽位 JSON；
#   - 含 "needs_clarify"（路由段 schema 标记）→ 意图路由段：按话语返回
#     澄清或放行；
#   - 其余 → 通用放行。
# 只依赖标准库；回复永不含真实密码（与哨兵协议一致，mock 只复述档案名）。
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

ROUTE_PASS = ('{"route":"export-data","sub":"","confidence":"high",'
              '"missing_slots":[],"out_of_scope":false,"needs_clarify":false,"reason":"导出意图"}')
ROUTE_CLARIFY_CONTENT = ('{"route":"export-data","sub":"","confidence":"high",'
                         '"missing_slots":["导出内容是什么"],"out_of_scope":false,'
                         '"needs_clarify":true,"reason":"缺导出内容"}')
ROUTE_CLARIFY_FORMAT = ('{"route":"export-data","sub":"","confidence":"high",'
                        '"missing_slots":["导出格式是什么"],"out_of_scope":false,'
                        '"needs_clarify":true,"reason":"缺格式"}')

# 场景 G：话语点名档案 oracle-scott → 槽位带 profile（服务端负责真实连接）。
SLOTS_PROFILE = ('{"scenario":"export","metadata":"database",'
                 '"source":{"profile":"oracle-scott","schema":"SCOTT"},'
                 '"export":{"format":"csv","tables":["emp"]}}')
# 场景 E：话语不带档案 → 空 source，页面选择器的默认档案（p2）兜底。
SLOTS_ALL = ('{"scenario":"export","metadata":"database","source":{},'
             '"export":{"format":"csv","tables":["*"]}}')


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # 验收输出只保留场景结果

    def do_GET(self):
        if self.path == '/health':
            self._json({"ok": True})
            return
        self._json({"error": {"message": "not found"}}, 404)

    def do_POST(self):
        if self.path != '/chat/completions':
            self._json({"error": {"message": "not found"}}, 404)
            return
        length = int(self.headers.get('Content-Length', 0))
        req = json.loads(self.rfile.read(length) or b'{}')
        msgs = req.get('messages') or []
        system = msgs[0].get('content', '') if msgs and msgs[0].get('role') == 'system' else ''
        # 路由/槽位两段的 user 消息都携带历史窗口，但当前话语总在
        # 「【用户最新一句话】」标记之后（见 serve.buildRouteUserMessage /
        # buildPlanUserMessage）——解析它做关键词判定，旧轮次不再污染。
        # 话语之后还可能拼接 facts block / 凭据说明（同样以「【」开头），
        # 一并截掉，否则 E 场景的档案清单会让话语误命中档案名。
        user_msgs = [m.get('content', '') for m in msgs if m.get('role') == 'user']
        joined = user_msgs[-1] if user_msgs else ''
        mark = '【用户最新一句话】'
        latest = joined.split(mark)[-1] if mark in joined else joined
        stop = latest.find('【')
        latest = (latest[:stop] if stop >= 0 else latest).strip()
        if 'SlotRequest' in system:
            content = SLOTS_PROFILE if 'oracle-scott' in latest else SLOTS_ALL
        elif 'needs_clarify' in system:
            if 'users' in latest:
                content = ROUTE_CLARIFY_CONTENT
            elif '导出表数据' in latest:
                content = ROUTE_CLARIFY_FORMAT
            else:
                content = ROUTE_PASS
        else:
            content = ROUTE_PASS
        self._json({
            "choices": [{"message": {"content": content}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 10},
        })

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18096
    HTTPServer(('127.0.0.1', port), Handler).serve_forever()
