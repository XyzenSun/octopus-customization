#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Octopus 管理面板 API 端到端测试。

特性:
    - 仅使用 Python 标准库 (urllib / json / argparse / time / uuid / sys / os)
    - 覆盖 doc/API.md 中 53 个接口的核心路径 (跳过外网依赖与转发路由)
    - 支持单文件独立运行: python3 api_test.py --base http://127.0.0.1:8080 \
                                              --user admin --password admin
    - 测试隔离: 创建的资源使用 test_<ts>_ 前缀, 测试结束统一清理
    - 退出码: 全部通过 0; 任意失败 1

注意:
    - 不调用 LLM 转发 /v1/chat/completions (无真实上游)
    - 不调用 /api/v1/channel/fetch-model, /api/v1/channel/sync (依赖外网)
    - 不调用 /api/v1/model/update-price (触发外网拉取)
    - 不调用 POST /api/v1/update (会触发自更新, 高风险)
    - SSE /api/v1/log/stream 仅测 stream-token, 不实际订阅 (避免阻塞)
    - /api/v1/setting/import 仅测 export, 不跑 import (写入风险)
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import traceback
import uuid
from typing import Any, Callable, Dict, List, Optional, Tuple
from urllib import error as urlerror
from urllib import parse as urlparse
from urllib import request as urlrequest


# =============================================================================
# 异常与断言工具
# =============================================================================


class ApiError(Exception):
    """API 调用错误, 包含 HTTP 状态、响应体与请求上下文。"""

    def __init__(
        self,
        message: str,
        *,
        status: int = 0,
        body: Any = None,
        method: str = "",
        path: str = "",
    ) -> None:
        super().__init__(message)
        self.status = status
        self.body = body
        self.method = method
        self.path = path


class AssertionFail(Exception):
    """断言失败异常, 用于在 case 内抛出。"""


def assert_eq(actual: Any, expected: Any, msg: str = "") -> None:
    """相等断言, 不相等抛 AssertionFail。"""
    if actual != expected:
        prefix = f"{msg}: " if msg else ""
        raise AssertionFail(
            f"{prefix}期望 {expected!r}, 实际 {actual!r}"
        )


def assert_status(resp: "Response", code: int) -> None:
    """断言 HTTP 状态码与统一响应外层 code 字段一致。"""
    if resp.status != code:
        raise AssertionFail(
            f"HTTP 状态码不符: 期望 {code}, 实际 {resp.status}; body={resp.text[:300]}"
        )
    if isinstance(resp.json, dict) and "code" in resp.json:
        if resp.json["code"] != code:
            raise AssertionFail(
                f"响应 code 字段不符: 期望 {code}, 实际 {resp.json['code']}"
            )


def assert_field_exists(obj: Any, *keys: str) -> None:
    """断言 dict 中包含全部指定字段。"""
    if not isinstance(obj, dict):
        raise AssertionFail(f"期望 dict, 实际 {type(obj).__name__}")
    for k in keys:
        if k not in obj:
            raise AssertionFail(f"缺少字段 {k!r}; 现有字段: {list(obj.keys())}")


def assert_truthy(value: Any, msg: str = "") -> None:
    """断言值为真。"""
    if not value:
        prefix = f"{msg}: " if msg else ""
        raise AssertionFail(f"{prefix}期望真值, 实际 {value!r}")


# =============================================================================
# HTTP 客户端
# =============================================================================


class Response:
    """简单响应包装。"""

    def __init__(self, status: int, headers: Dict[str, str], body: bytes) -> None:
        self.status = status
        self.headers = headers
        self.body = body
        self.text = body.decode("utf-8", errors="replace") if body else ""
        try:
            self.json: Any = json.loads(self.text) if self.text else None
        except json.JSONDecodeError:
            self.json = None


class Client:
    """轻量 HTTP 客户端, 封装鉴权头注入。"""

    def __init__(self, base: str, timeout: float = 30.0) -> None:
        self.base = base.rstrip("/")
        self.timeout = timeout
        self.jwt_token: Optional[str] = None
        self.api_key: Optional[str] = None

    # --------- 内部方法 ---------

    def _build_url(self, path: str, query: Optional[Dict[str, Any]]) -> str:
        if not path.startswith("/"):
            path = "/" + path
        url = self.base + path
        if query:
            qs = urlparse.urlencode(
                {k: v for k, v in query.items() if v is not None}, doseq=True
            )
            url = url + ("&" if "?" in url else "?") + qs
        return url

    def _build_headers(
        self,
        *,
        json_body: bool,
        auth: str,
        extra: Optional[Dict[str, str]],
    ) -> Dict[str, str]:
        headers: Dict[str, str] = {"Accept": "application/json"}
        if json_body:
            headers["Content-Type"] = "application/json"
        if auth == "jwt" and self.jwt_token:
            headers["Authorization"] = f"Bearer {self.jwt_token}"
        elif auth == "apikey" and self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"
        if extra:
            for k, v in extra.items():
                headers[k] = v
        return headers

    # --------- 公开接口 ---------

    def request(
        self,
        method: str,
        path: str,
        *,
        json_body: Optional[Any] = None,
        query: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
        auth: str = "jwt",
    ) -> Response:
        """发送 HTTP 请求, auth 取值: 'jwt' / 'apikey' / 'none'。"""
        url = self._build_url(path, query)
        body_bytes: Optional[bytes] = None
        if json_body is not None:
            body_bytes = json.dumps(json_body, ensure_ascii=False).encode("utf-8")
        all_headers = self._build_headers(
            json_body=json_body is not None, auth=auth, extra=headers
        )
        req = urlrequest.Request(
            url=url, data=body_bytes, method=method.upper(), headers=all_headers
        )
        try:
            with urlrequest.urlopen(req, timeout=self.timeout) as r:
                raw = r.read()
                rh = {k.lower(): v for k, v in r.getheaders()}
                return Response(r.status, rh, raw)
        except urlerror.HTTPError as e:
            raw = e.read() or b""
            rh = {k.lower(): v for k, v in (e.headers.items() if e.headers else [])}
            return Response(e.code, rh, raw)
        except urlerror.URLError as e:
            raise ApiError(
                f"无法连接 {url}: {e.reason}", method=method, path=path
            )
        except Exception as e:  # pragma: no cover
            raise ApiError(
                f"请求异常 {url}: {e}", method=method, path=path
            )


# =============================================================================
# 测试运行框架
# =============================================================================


class TestRunner:
    """轻量测试运行器: 注册 -> 顺序执行 -> 收集 -> 摘要。"""

    def __init__(self) -> None:
        self.cases: List[Tuple[str, Callable[[], None]]] = []
        self.results: List[Tuple[str, bool, str, float]] = []

    def add(self, name: str, fn: Callable[[], None]) -> None:
        self.cases.append((name, fn))

    def run(self) -> bool:
        total = len(self.cases)
        for idx, (name, fn) in enumerate(self.cases, 1):
            start = time.time()
            print(f"[{idx:>3}/{total}] 运行: {name} ... ", end="", flush=True)
            try:
                fn()
                cost = time.time() - start
                print(f"PASS ({cost:.2f}s)")
                self.results.append((name, True, "", cost))
            except AssertionFail as e:
                cost = time.time() - start
                print(f"FAIL ({cost:.2f}s)")
                self.results.append((name, False, f"AssertionFail: {e}", cost))
            except ApiError as e:
                cost = time.time() - start
                print(f"FAIL ({cost:.2f}s)")
                self.results.append((name, False, f"ApiError: {e}", cost))
            except Exception:  # pragma: no cover
                cost = time.time() - start
                tb = traceback.format_exc(limit=3)
                print(f"ERROR ({cost:.2f}s)")
                self.results.append((name, False, f"Exception:\n{tb}", cost))
        return self.report()

    def report(self) -> bool:
        passed = sum(1 for _, ok, _, _ in self.results if ok)
        failed = sum(1 for _, ok, _, _ in self.results if not ok)
        total_cost = sum(c for _, _, _, c in self.results)
        print()
        print("=" * 70)
        print(f"测试摘要: 通过 {passed} / 失败 {failed} / 总计 {len(self.results)}"
              f" / 耗时 {total_cost:.2f}s")
        print("=" * 70)
        if failed:
            print("失败明细:")
            for name, ok, msg, _ in self.results:
                if not ok:
                    print(f"  - {name}")
                    for line in msg.splitlines():
                        print(f"      {line}")
        return failed == 0


# =============================================================================
# 测试上下文 (跨 case 共享: 资源 ID、清理列表)
# =============================================================================


class Context:
    """测试上下文, 持有客户端、资源 ID 与清理回调。"""

    def __init__(self, client: Client, prefix: str) -> None:
        self.client = client
        self.prefix = prefix
        # 资源 ID 缓存
        self.channel_id: Optional[int] = None
        self.group_id: Optional[int] = None
        self.apikey_id: Optional[int] = None
        self.apikey_value: Optional[str] = None
        self.model_name: Optional[str] = None
        self.original_username: Optional[str] = None
        self.original_password: Optional[str] = None
        self.original_setting_value: Optional[str] = None
        # 清理任务列表 (后注册的先执行)
        self.cleanups: List[Tuple[str, Callable[[], None]]] = []

    def add_cleanup(self, name: str, fn: Callable[[], None]) -> None:
        self.cleanups.append((name, fn))

    def run_cleanups(self) -> None:
        """执行全部清理任务, 单个失败不影响其他。"""
        if not self.cleanups:
            return
        print()
        print("-" * 70)
        print("运行清理任务...")
        print("-" * 70)
        for name, fn in reversed(self.cleanups):
            try:
                fn()
                print(f"  cleanup OK: {name}")
            except Exception as e:  # pragma: no cover
                print(f"  cleanup FAIL: {name}: {e}")


# =============================================================================
# 用户模块测试 (User)
# =============================================================================


def make_user_cases(ctx: Context, username: str, password: str) -> List[Tuple[str, Callable[[], None]]]:
    """构造用户模块测试用例。"""
    c = ctx.client

    def case_login():
        # 登录, 拿到 JWT
        resp = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": username, "password": password, "expire": 1},
            auth="none",
        )
        assert_status(resp, 200)
        assert_truthy(isinstance(resp.json, dict), "响应应为 JSON 对象")
        assert_field_exists(resp.json, "code", "message", "data")
        data = resp.json["data"]
        assert_field_exists(data, "token", "expire_at")
        assert_truthy(data["token"], "token 不能为空")
        c.jwt_token = data["token"]
        ctx.original_username = username
        ctx.original_password = password

    def case_status_unauth():
        # 未鉴权访问 /status: Auth() 在 Authorization 缺失时返回 400 + ErrBadRequest
        c.jwt_token = None
        resp = c.request("GET", "/api/v1/user/status", auth="none")
        if resp.status not in (400, 401):
            raise AssertionFail(
                f"未鉴权访问期望 400/401, 实际 {resp.status}; body={resp.text[:200]}"
            )
        assert_truthy(isinstance(resp.json, dict), "应返回 JSON 包装")
        assert_field_exists(resp.json, "code", "message")

    def case_status_auth():
        # 重新登录后访问 /status, 期望 200/"ok"
        login_resp = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": username, "password": password, "expire": 1},
            auth="none",
        )
        assert_status(login_resp, 200)
        c.jwt_token = login_resp.json["data"]["token"]
        resp = c.request("GET", "/api/v1/user/status")
        assert_status(resp, 200)
        assert_eq(resp.json["data"], "ok", "status data 应为 'ok'")

    def case_change_password():
        # 改密码 -> 用新密码登录 -> 改回原值, 用原值登录验证
        new_pwd = f"{password}_x{int(time.time())}"
        r1 = c.request(
            "POST", "/api/v1/user/change-password",
            json_body={"old_password": password, "new_password": new_pwd},
        )
        assert_status(r1, 200)
        # 用新密码登录
        r2 = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": username, "password": new_pwd, "expire": 1},
            auth="none",
        )
        assert_status(r2, 200)
        c.jwt_token = r2.json["data"]["token"]
        # 改回原值
        r3 = c.request(
            "POST", "/api/v1/user/change-password",
            json_body={"old_password": new_pwd, "new_password": password},
        )
        assert_status(r3, 200)
        # 用原密码登录验证
        r4 = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": username, "password": password, "expire": 1},
            auth="none",
        )
        assert_status(r4, 200)
        c.jwt_token = r4.json["data"]["token"]

    def case_change_username():
        # 改用户名 -> 用新用户名登录 -> 改回原值
        new_user = f"{username}_t{int(time.time())}"
        r1 = c.request(
            "POST", "/api/v1/user/change-username",
            json_body={"new_username": new_user},
        )
        assert_status(r1, 200)
        # 用新用户名登录
        r2 = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": new_user, "password": password, "expire": 1},
            auth="none",
        )
        assert_status(r2, 200)
        c.jwt_token = r2.json["data"]["token"]
        # 改回原值
        r3 = c.request(
            "POST", "/api/v1/user/change-username",
            json_body={"new_username": username},
        )
        assert_status(r3, 200)
        # 用原用户名重登, 刷新 token
        r4 = c.request(
            "POST", "/api/v1/user/login",
            json_body={"username": username, "password": password, "expire": 1},
            auth="none",
        )
        assert_status(r4, 200)
        c.jwt_token = r4.json["data"]["token"]

    return [
        ("用户.登录拿到JWT", case_login),
        ("用户.未鉴权访问status", case_status_unauth),
        ("用户.鉴权后访问status", case_status_auth),
        ("用户.改密码后还原", case_change_password),
        ("用户.改用户名后还原", case_change_username),
    ]


# =============================================================================
# 渠道模块测试 (Channel)
# =============================================================================


def make_channel_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造渠道模块测试用例。"""
    c = ctx.client
    name_v1 = f"{ctx.prefix}_ch_{uuid.uuid4().hex[:6]}"
    name_v2 = name_v1 + "_renamed"

    def case_create():
        body = {
            "name": name_v1,
            "type": "openai_chat_completion",
            "enabled": True,
            "base_urls": [{"url": "https://api.example.test/v1", "delay": 0}],
            "keys": [{"enabled": True, "channel_key": "sk-test-fake", "remark": "init"}],
            "model": "gpt-4o",
            "custom_model": "",
            "proxy": False,
            "auto_sync": False,
            "auto_group": 0,
            "custom_header": [],
        }
        resp = c.request("POST", "/api/v1/channel/create", json_body=body)
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_field_exists(data, "id", "name", "type")
        assert_eq(data["name"], name_v1, "新建渠道名应一致")
        ctx.channel_id = data["id"]
        # 注册清理
        ctx.add_cleanup(
            f"删除测试渠道 id={ctx.channel_id}",
            lambda: c.request("DELETE", f"/api/v1/channel/delete/{ctx.channel_id}"),
        )

    def case_list_contains():
        resp = c.request("GET", "/api/v1/channel/list")
        assert_status(resp, 200)
        data = resp.json["data"] or []
        assert_truthy(isinstance(data, list), "渠道 list 应为数组")
        ids = [item.get("id") for item in data if isinstance(item, dict)]
        if ctx.channel_id not in ids:
            raise AssertionFail(f"列表未包含新建渠道 id={ctx.channel_id}")

    def case_update_rename():
        assert_truthy(ctx.channel_id, "前置: 渠道已创建")
        resp = c.request(
            "POST", "/api/v1/channel/update",
            json_body={"id": ctx.channel_id, "name": name_v2},
        )
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_eq(data["name"], name_v2, "更新后渠道名")

    def case_enable_toggle():
        assert_truthy(ctx.channel_id, "前置: 渠道已创建")
        # 禁用
        r1 = c.request(
            "POST", "/api/v1/channel/enable",
            json_body={"id": ctx.channel_id, "enabled": False},
        )
        assert_status(r1, 200)
        # 启用
        r2 = c.request(
            "POST", "/api/v1/channel/enable",
            json_body={"id": ctx.channel_id, "enabled": True},
        )
        assert_status(r2, 200)

    def case_last_sync_time():
        resp = c.request("GET", "/api/v1/channel/last-sync-time")
        assert_status(resp, 200)
        # data 是 time.Time JSON, 通常是 ISO 字符串或 zero time
        assert_truthy("data" in resp.json, "缺少 data 字段")

    def case_delete_and_verify():
        assert_truthy(ctx.channel_id, "前置: 渠道已创建")
        cid = ctx.channel_id
        r1 = c.request("DELETE", f"/api/v1/channel/delete/{cid}")
        assert_status(r1, 200)
        # 列表中已不存在
        r2 = c.request("GET", "/api/v1/channel/list")
        assert_status(r2, 200)
        data = r2.json["data"] or []
        ids = [item.get("id") for item in data if isinstance(item, dict)]
        if cid in ids:
            raise AssertionFail(f"删除后列表仍含 id={cid}")
        # 已经删除, 取消 cleanup 重复操作
        ctx.channel_id = None

    return [
        ("渠道.create", case_create),
        ("渠道.list包含新建", case_list_contains),
        ("渠道.update改名", case_update_rename),
        ("渠道.enable切换", case_enable_toggle),
        ("渠道.last-sync-time", case_last_sync_time),
        ("渠道.delete并验证不存在", case_delete_and_verify),
    ]


# =============================================================================
# 分组模块测试 (Group)
# =============================================================================


def make_group_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造分组模块测试用例。"""
    c = ctx.client
    name_v1 = f"{ctx.prefix}_grp_{uuid.uuid4().hex[:6]}"
    name_v2 = name_v1 + "_renamed"

    def case_create():
        body = {
            "name": name_v1,
            "mode": 1,  # GroupModeRoundRobin
            "match_regex": "",
            "first_token_time_out": 30,
            "session_keep_time": 0,
        }
        resp = c.request("POST", "/api/v1/group/create", json_body=body)
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_field_exists(data, "id", "name", "mode")
        ctx.group_id = data["id"]
        ctx.add_cleanup(
            f"删除测试分组 id={ctx.group_id}",
            lambda: c.request("DELETE", f"/api/v1/group/delete/{ctx.group_id}"),
        )

    def case_list_contains():
        resp = c.request("GET", "/api/v1/group/list")
        assert_status(resp, 200)
        data = resp.json["data"] or []
        ids = [item.get("id") for item in data if isinstance(item, dict)]
        if ctx.group_id not in ids:
            raise AssertionFail(f"分组列表未包含 id={ctx.group_id}")

    def case_update_rename():
        assert_truthy(ctx.group_id, "前置: 分组已创建")
        resp = c.request(
            "POST", "/api/v1/group/update",
            json_body={"id": ctx.group_id, "name": name_v2},
        )
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_eq(data["name"], name_v2, "分组改名后")

    def case_delete():
        assert_truthy(ctx.group_id, "前置: 分组已创建")
        gid = ctx.group_id
        resp = c.request("DELETE", f"/api/v1/group/delete/{gid}")
        assert_status(resp, 200)
        # data 为字符串 "group deleted successfully"
        assert_eq(resp.json["data"], "group deleted successfully", "分组删除提示")
        ctx.group_id = None

    return [
        ("分组.create", case_create),
        ("分组.list包含新建", case_list_contains),
        ("分组.update改名", case_update_rename),
        ("分组.delete", case_delete),
    ]


# =============================================================================
# API Key 模块测试 (APIKey)
# =============================================================================


def make_apikey_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造 API Key 模块测试用例。"""
    c = ctx.client
    name_v1 = f"{ctx.prefix}_ak_{uuid.uuid4().hex[:6]}"
    name_v2 = name_v1 + "_renamed"

    def case_create():
        body = {
            "name": name_v1,
            "api_key": "ignored-by-server",  # 服务端会覆盖
            "enabled": True,
            "expire_at": 0,
            "max_cost": 0,
            "supported_models": "",
        }
        resp = c.request("POST", "/api/v1/apikey/create", json_body=body)
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_field_exists(data, "id", "name", "api_key", "enabled")
        ctx.apikey_id = data["id"]
        ctx.apikey_value = data["api_key"]
        # 校验后端生成的 api_key 前缀
        if not isinstance(ctx.apikey_value, str) or not ctx.apikey_value.startswith("sk-octopus-"):
            raise AssertionFail(
                f"api_key 前缀应为 sk-octopus-, 实际 {ctx.apikey_value!r}"
            )
        c.api_key = ctx.apikey_value
        ctx.add_cleanup(
            f"删除测试 APIKey id={ctx.apikey_id}",
            lambda: c.request("DELETE", f"/api/v1/apikey/delete/{ctx.apikey_id}"),
        )

    def case_list_contains():
        resp = c.request("GET", "/api/v1/apikey/list")
        assert_status(resp, 200)
        data = resp.json["data"] or []
        ids = [item.get("id") for item in data if isinstance(item, dict)]
        if ctx.apikey_id not in ids:
            raise AssertionFail(f"APIKey 列表未包含 id={ctx.apikey_id}")

    def case_apikey_login_probe():
        # 用 API Key 鉴权探测 GET /api/v1/apikey/login
        assert_truthy(ctx.apikey_value, "前置: APIKey 已创建")
        resp = c.request("GET", "/api/v1/apikey/login", auth="apikey")
        assert_status(resp, 200)

    def case_apikey_stats():
        # 用 API Key 鉴权请求 GET /api/v1/apikey/stats
        assert_truthy(ctx.apikey_value, "前置: APIKey 已创建")
        resp = c.request("GET", "/api/v1/apikey/stats", auth="apikey")
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_field_exists(data, "stats", "info")
        # info 字段含 id 与当前 APIKey id 一致
        assert_eq(data["info"]["id"], ctx.apikey_id, "stats.info.id")
        assert_truthy(isinstance(data["stats"], dict), "stats 应为对象")

    def case_v1_models_openai():
        # GET /v1/models 默认 openai 协议
        assert_truthy(ctx.apikey_value, "前置: APIKey 已创建")
        resp = c.request("GET", "/v1/models", auth="apikey")
        if resp.status != 200:
            raise AssertionFail(
                f"GET /v1/models 期望 200, 实际 {resp.status}; body={resp.text[:200]}"
            )
        # 不走 ResponseStruct 包装, 应直接是 {object: list, data: [...]}
        assert_truthy(isinstance(resp.json, dict), "v1/models 响应应为对象")
        assert_field_exists(resp.json, "data", "object")
        assert_eq(resp.json["object"], "list", "openai object 字段")

    def case_v1_models_anthropic():
        # GET /v1/models 显式指定 x-api-key (anthropic 协议)
        assert_truthy(ctx.apikey_value, "前置: APIKey 已创建")
        resp = c.request(
            "GET", "/v1/models",
            auth="none",
            headers={"x-api-key": ctx.apikey_value},
        )
        if resp.status != 200:
            raise AssertionFail(
                f"GET /v1/models (anthropic) 期望 200, 实际 {resp.status};"
                f" body={resp.text[:200]}"
            )
        assert_truthy(isinstance(resp.json, dict), "anthropic 响应应为对象")
        # anthropic 格式: {data: [...], has_more: bool, ...}
        assert_field_exists(resp.json, "data", "has_more")

    def case_update_rename():
        assert_truthy(ctx.apikey_id, "前置: APIKey 已创建")
        body = {
            "id": ctx.apikey_id,
            "name": name_v2,
            "api_key": ctx.apikey_value,
            "enabled": True,
        }
        resp = c.request("POST", "/api/v1/apikey/update", json_body=body)
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_eq(data["name"], name_v2, "APIKey 改名后")

    def case_delete():
        assert_truthy(ctx.apikey_id, "前置: APIKey 已创建")
        kid = ctx.apikey_id
        resp = c.request("DELETE", f"/api/v1/apikey/delete/{kid}")
        assert_status(resp, 200)
        # 列表中应不再存在
        r2 = c.request("GET", "/api/v1/apikey/list")
        assert_status(r2, 200)
        data = r2.json["data"] or []
        ids = [item.get("id") for item in data if isinstance(item, dict)]
        if kid in ids:
            raise AssertionFail(f"删除后 APIKey 列表仍含 id={kid}")
        ctx.apikey_id = None
        ctx.apikey_value = None
        c.api_key = None

    return [
        ("APIKey.create", case_create),
        ("APIKey.list包含新建", case_list_contains),
        ("APIKey.login探测(API Key 鉴权)", case_apikey_login_probe),
        ("APIKey.stats(API Key 鉴权)", case_apikey_stats),
        ("APIKey./v1/models openai 协议", case_v1_models_openai),
        ("APIKey./v1/models anthropic 协议", case_v1_models_anthropic),
        ("APIKey.update改名", case_update_rename),
        ("APIKey.delete并验证不存在", case_delete),
    ]


# =============================================================================
# 日志模块测试 (Log)
# =============================================================================


def make_log_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造日志模块测试用例。"""
    c = ctx.client

    def case_list():
        resp = c.request(
            "GET", "/api/v1/log/list",
            query={"page": 1, "page_size": 20},
        )
        assert_status(resp, 200)
        # data 可能为 null 或数组
        data = resp.json.get("data")
        assert_truthy(data is None or isinstance(data, list), "log/list data 为 list 或 null")

    def case_stream_token():
        resp = c.request("GET", "/api/v1/log/stream-token")
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_field_exists(data, "token")
        assert_truthy(data["token"], "stream token 非空")

    def case_clear():
        resp = c.request("DELETE", "/api/v1/log/clear")
        assert_status(resp, 200)

    return [
        ("日志.list分页", case_list),
        ("日志.stream-token", case_stream_token),
        ("日志.clear", case_clear),
    ]


# =============================================================================
# 设置模块测试 (Setting)
# =============================================================================


def make_setting_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造设置模块测试用例。"""
    c = ctx.client
    target_key = "relay_log_keep_period"

    def case_list_and_save_original():
        resp = c.request("GET", "/api/v1/setting/list")
        assert_status(resp, 200)
        data = resp.json["data"] or []
        assert_truthy(isinstance(data, list), "setting list 为数组")
        for item in data:
            if isinstance(item, dict) and item.get("key") == target_key:
                ctx.original_setting_value = str(item.get("value", "7"))
                break
        if ctx.original_setting_value is None:
            ctx.original_setting_value = "7"  # 默认值兜底
        ctx.add_cleanup(
            f"还原设置 {target_key}={ctx.original_setting_value}",
            lambda: c.request(
                "POST", "/api/v1/setting/set",
                json_body={"key": target_key, "value": ctx.original_setting_value},
            ),
        )

    def case_set_and_verify():
        resp = c.request(
            "POST", "/api/v1/setting/set",
            json_body={"key": target_key, "value": "7"},
        )
        assert_status(resp, 200)
        data = resp.json["data"]
        assert_eq(data["key"], target_key, "set 返回 key 一致")
        assert_eq(str(data["value"]), "7", "set 返回 value 一致")
        # list 中也应反映
        r2 = c.request("GET", "/api/v1/setting/list")
        assert_status(r2, 200)
        found = False
        for item in r2.json["data"] or []:
            if isinstance(item, dict) and item.get("key") == target_key:
                if str(item.get("value")) == "7":
                    found = True
                break
        if not found:
            raise AssertionFail(f"setting list 未反映 {target_key}=7")

    def case_export_no_logs_no_stats():
        resp = c.request(
            "GET", "/api/v1/setting/export",
            query={"include_logs": "false", "include_stats": "false"},
        )
        assert_status(resp, 200)
        cd = resp.headers.get("content-disposition", "")
        if "attachment" not in cd:
            raise AssertionFail(
                f"export 应返回 Content-Disposition: attachment, 实际 {cd!r}"
            )
        # 响应体应为合法 JSON (DBDump 直接序列化, 无 ResponseStruct 包装)
        if not isinstance(resp.json, dict):
            raise AssertionFail(
                f"export body 应为 JSON 对象, 实际 {type(resp.json).__name__}"
            )
        assert_field_exists(resp.json, "version", "exported_at")

    return [
        ("设置.list并保存原值", case_list_and_save_original),
        ("设置.set 并 list 验证", case_set_and_verify),
        ("设置.export 不含 logs/stats", case_export_no_logs_no_stats),
    ]


# =============================================================================
# 模型模块测试 (Model / LLM)
# =============================================================================


def make_model_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造模型模块测试用例。"""
    c = ctx.client
    model_name = f"{ctx.prefix}_model_{uuid.uuid4().hex[:6]}"

    def case_create():
        body = {
            "name": model_name,
            "input": 1.0,
            "output": 2.0,
            "cache_read": 0.5,
            "cache_write": 1.5,
        }
        resp = c.request("POST", "/api/v1/model/create", json_body=body)
        assert_status(resp, 200)
        ctx.model_name = model_name
        ctx.add_cleanup(
            f"删除测试模型 name={model_name}",
            lambda: c.request(
                "POST", "/api/v1/model/delete",
                json_body={"name": ctx.model_name or model_name},
            ),
        )

    def case_list_contains():
        resp = c.request("GET", "/api/v1/model/list")
        assert_status(resp, 200)
        data = resp.json["data"] or []
        names = [item.get("name") for item in data if isinstance(item, dict)]
        if model_name not in names:
            raise AssertionFail(f"model list 未包含 {model_name}")

    def case_update():
        assert_truthy(ctx.model_name, "前置: 模型已创建")
        body = {
            "name": ctx.model_name,
            "input": 3.0,
            "output": 4.0,
            "cache_read": 0.5,
            "cache_write": 1.5,
        }
        resp = c.request("POST", "/api/v1/model/update", json_body=body)
        assert_status(resp, 200)

    def case_channel_view():
        resp = c.request("GET", "/api/v1/model/channel")
        assert_status(resp, 200)
        data = resp.json.get("data")
        # data 可能为 null (无渠道) 或数组
        assert_truthy(data is None or isinstance(data, list), "model/channel data 为 list 或 null")

    def case_last_update_time():
        resp = c.request("GET", "/api/v1/model/last-update-time")
        assert_status(resp, 200)

    def case_delete_and_verify():
        assert_truthy(ctx.model_name, "前置: 模型已创建")
        name = ctx.model_name
        resp = c.request(
            "POST", "/api/v1/model/delete",
            json_body={"name": name},
        )
        assert_status(resp, 200)
        r2 = c.request("GET", "/api/v1/model/list")
        assert_status(r2, 200)
        data = r2.json["data"] or []
        names = [item.get("name") for item in data if isinstance(item, dict)]
        if name in names:
            raise AssertionFail(f"删除后 model list 仍含 {name}")
        ctx.model_name = None

    return [
        ("模型.create", case_create),
        ("模型.list包含新建", case_list_contains),
        ("模型.update改 input", case_update),
        ("模型.channel 视图", case_channel_view),
        ("模型.last-update-time", case_last_update_time),
        ("模型.delete并验证不存在", case_delete_and_verify),
    ]


# =============================================================================
# 统计模块测试 (Stats)
# =============================================================================


def make_stats_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造统计模块测试用例。"""
    c = ctx.client

    def _check(path: str, allow_list: bool = False) -> None:
        resp = c.request("GET", path)
        assert_status(resp, 200)
        data = resp.json.get("data")
        if allow_list:
            assert_truthy(data is None or isinstance(data, list), f"{path} data 为 list 或 null")
        else:
            assert_truthy(
                data is None or isinstance(data, (dict, list)),
                f"{path} data 为对象/数组/null",
            )

    return [
        ("统计.today", lambda: _check("/api/v1/stats/today")),
        ("统计.total", lambda: _check("/api/v1/stats/total")),
        ("统计.daily", lambda: _check("/api/v1/stats/daily", allow_list=True)),
        ("统计.hourly", lambda: _check("/api/v1/stats/hourly", allow_list=True)),
        ("统计.apikey", lambda: _check("/api/v1/stats/apikey", allow_list=True)),
    ]


# =============================================================================
# 更新模块测试 (Update)
# =============================================================================


def make_update_cases(ctx: Context) -> List[Tuple[str, Callable[[], None]]]:
    """构造更新模块测试用例 (仅 GET /now-version, 不触发自更新)。"""
    c = ctx.client

    def case_now_version():
        resp = c.request("GET", "/api/v1/update/now-version")
        assert_status(resp, 200)
        data = resp.json.get("data")
        assert_truthy(isinstance(data, str) and len(data) > 0, "now-version 应为非空字符串")

    return [
        ("更新.now-version", case_now_version),
    ]


# =============================================================================
# 主入口
# =============================================================================


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Octopus 管理面板 API 端到端测试",
    )
    parser.add_argument(
        "--base", default=os.environ.get("OCTOPUS_TEST_BASE", "http://127.0.0.1:8080"),
        help="后端基地址 (默认 http://127.0.0.1:8080, 可用 OCTOPUS_TEST_BASE 覆盖)",
    )
    parser.add_argument(
        "--user", default=os.environ.get("OCTOPUS_TEST_USER", "admin"),
        help="登录用户名 (默认 admin)",
    )
    parser.add_argument(
        "--password", default=os.environ.get("OCTOPUS_TEST_PASSWORD", "admin"),
        help="登录密码 (默认 admin)",
    )
    parser.add_argument(
        "--timeout", type=float, default=30.0,
        help="单次 HTTP 超时秒数 (默认 30)",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    print("=" * 70)
    print(f"Octopus API 测试")
    print(f"  base    = {args.base}")
    print(f"  user    = {args.user}")
    print(f"  timeout = {args.timeout}s")
    print("=" * 70)

    client = Client(args.base, timeout=args.timeout)
    prefix = f"test_{int(time.time())}"
    ctx = Context(client, prefix=prefix)
    runner = TestRunner()

    # 注册全部 case (顺序敏感)
    for name, fn in make_user_cases(ctx, args.user, args.password):
        runner.add(name, fn)
    for name, fn in make_channel_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_group_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_apikey_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_log_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_setting_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_model_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_stats_cases(ctx):
        runner.add(name, fn)
    for name, fn in make_update_cases(ctx):
        runner.add(name, fn)

    try:
        ok = runner.run()
    finally:
        # 无论成败, 都尝试清理已创建资源
        # 清理需要 JWT token, 若 token 失效则尝试重新登录一次
        if not client.jwt_token:
            try:
                login_resp = client.request(
                    "POST", "/api/v1/user/login",
                    json_body={
                        "username": args.user,
                        "password": args.password,
                        "expire": 1,
                    },
                    auth="none",
                )
                if (
                    login_resp.status == 200
                    and isinstance(login_resp.json, dict)
                    and isinstance(login_resp.json.get("data"), dict)
                ):
                    client.jwt_token = login_resp.json["data"].get("token")
            except Exception:  # pragma: no cover
                pass
        ctx.run_cleanups()

    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

