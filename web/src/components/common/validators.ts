import { toast } from './Toast';

/**
 * 校验 param_override：非空字符串必须能解析成 JSON object（顶层数组/标量均视为非法）。
 * 与后端 handlers/param_override.go 的口径保持一致——只管格式，不管内容语义。
 * 校验失败时弹出错误提示并返回 false，调用方据此中止后续提交。
 */
export function isValidParamOverride(raw: string, invalidMessage: string): boolean {
    const trimmed = raw.trim();
    if (trimmed === '') return true;
    let valid = false;
    try {
        const parsed: unknown = JSON.parse(trimmed);
        valid = typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed);
    } catch {
        valid = false;
    }
    if (!valid) {
        toast.error(invalidMessage);
    }
    return valid;
}
