#!/usr/bin/env bash
# -----------------------------------------------------------------------------
# Octopus 构建产物完备测试
#
# 阶段:
#   1. 环境检测  : 检查 go / pnpm / node / python3 / curl / file 命令存在
#   2. 执行构建  : bash scripts/build.sh build linux x86_64
#   3. 产物校验  : 二进制存在、大小、ELF 标识、static/out 前端产物
#   4. 版本信息  : ./build/bin/octopus-linux-x86_64 version 输出非空
#   5. 运行时探活: 临时数据目录启动、未鉴权 401、登录后 200/"ok"、SIGTERM 关闭
#   6. 清理       : trap EXIT 删临时目录、kill 残留 PID
#
# 使用:
#   bash .github/test/build_test.sh
#   (必须在仓库根目录或可访问 scripts/build.sh 的工作目录运行)
#
# 退出码:
#   0 - 全部检查通过
#   非 0 - 步骤失败 (见标准错误输出)
# -----------------------------------------------------------------------------

set -euo pipefail

# =============================================================================
# 全局变量与日志工具
# =============================================================================

# 颜色 (TTY 时启用)
if [ -t 1 ]; then
    C_RED=$'\033[31m'
    C_GREEN=$'\033[32m'
    C_YELLOW=$'\033[33m'
    C_BLUE=$'\033[34m'
    C_RESET=$'\033[0m'
else
    C_RED=""
    C_GREEN=""
    C_YELLOW=""
    C_BLUE=""
    C_RESET=""
fi

# 测试统计
PASSED=0
FAILED=0
TOTAL=0
START_TS=$(date +%s)

# 临时目录与运行时 PID (cleanup 使用)
TEST_TMP_DIR=""
SERVER_PID=""

# 仓库根目录 (脚本可从任意目录调用)
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# 二进制路径
BIN_PATH="${REPO_ROOT}/build/bin/octopus-linux-x86_64"
STATIC_DIR="${REPO_ROOT}/static/out"

# 端口选择: 默认 38080, 可被 OCTOPUS_TEST_PORT 覆盖
TEST_PORT="${OCTOPUS_TEST_PORT:-38080}"

log_info()  { printf "%s[INFO]%s  %s\n"  "${C_BLUE}"   "${C_RESET}" "$*"; }
log_pass()  { printf "%s[PASS]%s  %s\n"  "${C_GREEN}"  "${C_RESET}" "$*"; }
log_warn()  { printf "%s[WARN]%s  %s\n"  "${C_YELLOW}" "${C_RESET}" "$*" >&2; }
log_fail()  { printf "%s[FAIL]%s  %s\n"  "${C_RED}"    "${C_RESET}" "$*" >&2; }
log_step()  {
    printf "\n%s======================================================================%s\n" \
        "${C_BLUE}" "${C_RESET}"
    printf "%s>>>>%s %s\n" "${C_BLUE}" "${C_RESET}" "$*"
    printf "%s======================================================================%s\n" \
        "${C_BLUE}" "${C_RESET}"
}

# 检查项注册器
check_pass() {
    PASSED=$((PASSED + 1))
    TOTAL=$((TOTAL + 1))
    log_pass "$*"
}
check_fail() {
    FAILED=$((FAILED + 1))
    TOTAL=$((TOTAL + 1))
    log_fail "$*"
}

# =============================================================================
# 清理函数 (trap EXIT)
# =============================================================================

cleanup() {
    local exit_code=$?
    log_step "清理资源"

    # 停止后台进程
    if [ -n "${SERVER_PID:-}" ] && kill -0 "${SERVER_PID}" 2>/dev/null; then
        log_info "向 PID=${SERVER_PID} 发送 SIGTERM"
        kill -TERM "${SERVER_PID}" 2>/dev/null || true
        # 最多等 10 秒
        local i
        for i in 1 2 3 4 5 6 7 8 9 10; do
            if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
                break
            fi
            sleep 1
        done
        if kill -0 "${SERVER_PID}" 2>/dev/null; then
            log_warn "PID=${SERVER_PID} 未在 10s 内退出, 发送 SIGKILL"
            kill -KILL "${SERVER_PID}" 2>/dev/null || true
        fi
    fi

    # 失败时打印 server 日志末尾
    if [ "${exit_code}" -ne 0 ] && [ -n "${TEST_TMP_DIR:-}" ] \
       && [ -f "${TEST_TMP_DIR}/server.log" ]; then
        log_warn "服务器日志末尾 50 行 (${TEST_TMP_DIR}/server.log):"
        tail -n 50 "${TEST_TMP_DIR}/server.log" >&2 || true
    fi

    # 删除临时目录
    if [ -n "${TEST_TMP_DIR:-}" ] && [ -d "${TEST_TMP_DIR}" ]; then
        rm -rf "${TEST_TMP_DIR}" || true
        log_info "已删除临时目录 ${TEST_TMP_DIR}"
    fi

    print_summary "${exit_code}"
}

trap cleanup EXIT INT TERM

print_summary() {
    local exit_code="${1:-0}"
    local end_ts
    end_ts=$(date +%s)
    local cost=$((end_ts - START_TS))
    printf "\n%s======================================================================%s\n" \
        "${C_BLUE}" "${C_RESET}"
    printf "构建测试摘要: 通过 %d / 失败 %d / 总计 %d / 耗时 %ds\n" \
        "${PASSED}" "${FAILED}" "${TOTAL}" "${cost}"
    printf "%s======================================================================%s\n" \
        "${C_BLUE}" "${C_RESET}"
    if [ "${FAILED}" -gt 0 ]; then
        printf "%s整体: FAIL%s (退出码 %d)\n" "${C_RED}" "${C_RESET}" "${exit_code}"
    elif [ "${exit_code}" -ne 0 ]; then
        printf "%s整体: 异常退出%s (退出码 %d)\n" "${C_RED}" "${C_RESET}" "${exit_code}"
    else
        printf "%s整体: PASS%s\n" "${C_GREEN}" "${C_RESET}"
    fi
}

# =============================================================================
# Step 1: 环境检测
# =============================================================================

check_command() {
    local cmd="$1"
    if command -v "${cmd}" >/dev/null 2>&1; then
        local v
        v=$("${cmd}" --version 2>/dev/null | head -n 1 || echo "(unknown)")
        check_pass "${cmd} 已安装: ${v}"
    else
        check_fail "${cmd} 未安装"
        return 1
    fi
}

step_check_env() {
    log_step "Step 1: 环境检测"
    local missing=0
    for cmd in go pnpm node python3 curl file; do
        if ! check_command "${cmd}"; then
            missing=$((missing + 1))
        fi
    done
    if [ "${missing}" -gt 0 ]; then
        log_fail "缺少 ${missing} 个必备命令, 终止测试"
        exit 2
    fi
}

# =============================================================================
# Step 2: 执行构建
# =============================================================================

step_build() {
    log_step "Step 2: 执行构建 (build linux x86_64)"
    cd "${REPO_ROOT}"

    if [ ! -x scripts/build.sh ] && [ ! -f scripts/build.sh ]; then
        check_fail "scripts/build.sh 不存在"
        exit 3
    fi

    log_info "在 ${REPO_ROOT} 运行: bash scripts/build.sh build linux x86_64"
    if bash scripts/build.sh build linux x86_64; then
        check_pass "构建命令退出码 0"
    else
        local rc=$?
        check_fail "构建命令失败, 退出码 ${rc}"
        exit "${rc}"
    fi
}

# =============================================================================
# Step 3: 产物校验
# =============================================================================

step_check_artifacts() {
    log_step "Step 3: 产物校验"

    # 二进制存在
    if [ -f "${BIN_PATH}" ]; then
        check_pass "二进制存在: ${BIN_PATH}"
    else
        check_fail "缺失二进制: ${BIN_PATH}"
        return 1
    fi

    # 二进制可执行
    if [ -x "${BIN_PATH}" ]; then
        check_pass "二进制可执行 (x 位)"
    else
        check_fail "二进制无可执行权限"
    fi

    # 二进制大小 > 5 MiB (含前端 embed, 通常 ~30 MiB+)
    local min_bytes=$((5 * 1024 * 1024))
    local size
    size=$(stat -c '%s' "${BIN_PATH}" 2>/dev/null || stat -f '%z' "${BIN_PATH}" 2>/dev/null || echo 0)
    if [ "${size}" -gt "${min_bytes}" ]; then
        check_pass "二进制大小 ${size} bytes ($(( size / 1024 / 1024 )) MiB) > 5 MiB"
    else
        check_fail "二进制大小 ${size} bytes 未达到 5 MiB"
    fi

    # ELF 64-bit LSB
    local file_out
    file_out=$(file "${BIN_PATH}" 2>/dev/null || echo "")
    if printf "%s" "${file_out}" | grep -q "ELF 64-bit LSB"; then
        check_pass "二进制文件类型: ELF 64-bit LSB"
    else
        check_fail "二进制文件类型不符: ${file_out}"
    fi

    # 前端产物 static/out/index.html 存在
    if [ -f "${STATIC_DIR}/index.html" ]; then
        check_pass "前端产物存在: ${STATIC_DIR}/index.html"
    else
        check_fail "前端产物缺失: ${STATIC_DIR}/index.html"
    fi
}

# =============================================================================
# Step 4: 版本信息
# =============================================================================

step_version_info() {
    log_step "Step 4: 版本信息"
    local out
    if ! out=$("${BIN_PATH}" version 2>&1); then
        check_fail "version 子命令执行失败: ${out}"
        return 1
    fi
    if [ -z "${out}" ]; then
        check_fail "version 输出为空"
        return 1
    fi
    log_info "version 输出:"
    printf "%s\n" "${out}" | sed 's/^/    /'
    check_pass "version 输出非空 (含 $(printf "%s" "${out}" | wc -l) 行)"
}

# =============================================================================
# Step 5: 运行时探活
# =============================================================================

# 解析 JSON 字段值 (复用 python3, 已是必备依赖)
json_field() {
    # 用法: json_field <json-string> <field-name>
    local payload="$1"
    local field="$2"
    python3 -c "
import json, sys
try:
    d = json.loads(sys.argv[1])
    if isinstance(d, dict):
        v = d.get(sys.argv[2])
        if v is None:
            sys.exit(0)
        if isinstance(v, (dict, list)):
            print(json.dumps(v))
        else:
            print(v)
except Exception:
    sys.exit(0)
" "${payload}" "${field}" 2>/dev/null
}

# 嵌套字段
json_nested() {
    # 用法: json_nested <json-string> <key1> <key2>
    python3 -c "
import json, sys
try:
    d = json.loads(sys.argv[1])
    cur = d
    for k in sys.argv[2:]:
        if isinstance(cur, dict):
            cur = cur.get(k)
        else:
            cur = None
            break
    if cur is None:
        sys.exit(0)
    if isinstance(cur, (dict, list)):
        print(json.dumps(cur))
    else:
        print(cur)
except Exception:
    sys.exit(0)
" "$@"
}

step_runtime_probe() {
    log_step "Step 5: 运行时探活"

    # 临时目录 (mktemp, 失败立即退出)
    TEST_TMP_DIR=$(mktemp -d 2>/dev/null || mktemp -d -t octopus-build-test)
    if [ -z "${TEST_TMP_DIR}" ] || [ ! -d "${TEST_TMP_DIR}" ]; then
        check_fail "mktemp 创建临时目录失败"
        exit 4
    fi
    log_info "临时目录: ${TEST_TMP_DIR}"

    # 写入测试用配置 (避免污染 ./data/)
    local cfg="${TEST_TMP_DIR}/config.json"
    cat >"${cfg}" <<EOF
{
  "server":   { "host": "127.0.0.1", "port": ${TEST_PORT} },
  "database": { "type": "sqlite", "path": "${TEST_TMP_DIR}/test.db" },
  "log":      { "level": "warn" }
}
EOF
    log_info "配置文件: ${cfg} (port=${TEST_PORT})"

    # 后台启动服务
    local log_file="${TEST_TMP_DIR}/server.log"
    "${BIN_PATH}" start --config "${cfg}" >"${log_file}" 2>&1 &
    SERVER_PID=$!
    log_info "已后台启动服务, PID=${SERVER_PID}"

    # 等待服务就绪 (最多 30s, 每秒探测)
    local base="http://127.0.0.1:${TEST_PORT}"
    local ready=0
    local i
    for i in $(seq 1 30); do
        if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
            check_fail "服务进程已退出 (i=${i})"
            return 1
        fi
        # status 接口未鉴权时返回 400/401, 但能连上即说明 HTTP 可用
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
            "${base}/api/v1/user/status" 2>/dev/null || echo "000")
        if [ "${code}" != "000" ]; then
            ready=1
            log_info "服务就绪 (i=${i}, code=${code})"
            break
        fi
        sleep 1
    done

    if [ "${ready}" -ne 1 ]; then
        check_fail "服务在 30s 内未就绪 (端口 ${TEST_PORT})"
        return 1
    else
        check_pass "服务在 ${i}s 内就绪"
    fi

    # 子检查 1: 未鉴权访问 /status 应返回 4xx 且 body 含 code 字段
    local unauth_body unauth_code
    unauth_code=$(curl -s -o "${TEST_TMP_DIR}/unauth.body" \
        -w '%{http_code}' --max-time 5 \
        "${base}/api/v1/user/status" 2>/dev/null || echo "000")
    unauth_body=$(cat "${TEST_TMP_DIR}/unauth.body" 2>/dev/null || echo "")
    if [ "${unauth_code}" = "401" ] || [ "${unauth_code}" = "400" ]; then
        check_pass "未鉴权 GET /status 返回 ${unauth_code}"
    else
        check_fail "未鉴权 GET /status 期望 400/401, 实际 ${unauth_code}; body=${unauth_body}"
    fi
    local code_field
    code_field=$(json_field "${unauth_body}" "code" || true)
    if [ -n "${code_field}" ]; then
        check_pass "未鉴权响应体含 code 字段 (=${code_field})"
    else
        check_fail "未鉴权响应体缺少 code 字段; body=${unauth_body}"
    fi

    # 子检查 2: admin/admin 登录拿 token
    local login_body login_code
    login_code=$(curl -s -o "${TEST_TMP_DIR}/login.body" \
        -w '%{http_code}' --max-time 5 \
        -X POST -H 'Content-Type: application/json' \
        -d '{"username":"admin","password":"admin","expire":1}' \
        "${base}/api/v1/user/login" 2>/dev/null || echo "000")
    login_body=$(cat "${TEST_TMP_DIR}/login.body" 2>/dev/null || echo "")
    if [ "${login_code}" != "200" ]; then
        check_fail "登录请求失败, code=${login_code}; body=${login_body}"
        return 1
    fi
    local token
    token=$(json_nested "${login_body}" data token 2>/dev/null || true)
    if [ -n "${token}" ]; then
        check_pass "登录成功, token 长度=${#token}"
    else
        check_fail "登录响应缺少 data.token; body=${login_body}"
        return 1
    fi

    # 子检查 3: 带 Bearer token 访问 /status, 期望 200/"ok"
    local auth_body auth_code
    auth_code=$(curl -s -o "${TEST_TMP_DIR}/auth.body" \
        -w '%{http_code}' --max-time 5 \
        -H "Authorization: Bearer ${token}" \
        "${base}/api/v1/user/status" 2>/dev/null || echo "000")
    auth_body=$(cat "${TEST_TMP_DIR}/auth.body" 2>/dev/null || echo "")
    if [ "${auth_code}" = "200" ]; then
        check_pass "鉴权后 GET /status 返回 200"
    else
        check_fail "鉴权后 GET /status 期望 200, 实际 ${auth_code}; body=${auth_body}"
    fi
    local data_field
    data_field=$(json_field "${auth_body}" "data" || true)
    if [ "${data_field}" = "ok" ]; then
        check_pass '鉴权响应 data 字段为 "ok"'
    else
        check_fail "鉴权响应 data 字段期望 'ok', 实际 '${data_field}'; body=${auth_body}"
    fi

    # SIGTERM 关闭由 cleanup() 处理 (trap EXIT)
}

# =============================================================================
# 主入口
# =============================================================================

main() {
    log_step "Octopus 构建产物完备测试 (REPO=${REPO_ROOT})"

    step_check_env
    step_build
    step_check_artifacts
    step_version_info
    step_runtime_probe

    if [ "${FAILED}" -gt 0 ]; then
        exit 1
    fi
    exit 0
}

main "$@"
