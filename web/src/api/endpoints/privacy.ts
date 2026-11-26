import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';

/** 隐私处理范围位掩码, 与后端配置保持一致. */
export const PrivacyScope = {
    Messages: 1,
    ToolDefinitions: 2,
    ToolCalls: 4,
    ToolResponses: 8,
} as const;

export type PrivacyFailurePolicy = 'continue' | 'abort';

/** 管理接口返回明文规则, 不将规则或请求对象写入日志. */
export interface PrivacyRule {
    id: number;
    source: string;
    alias: string;
    enabled: boolean;
}

export interface PrivacyRuleCreate {
    /** 仅兼容后端 Go int 解码, 创建不会采用客户端 ID. */
    id?: number | null;
    source: string;
    alias: string;
    enabled?: boolean;
}

/** 完整更新, 启停也必须提交所有字段. */
export type PrivacyRuleUpdate = PrivacyRule;

export const privacyRuleListQueryKey = ['privacy-rules', 'list'] as const;

/** 获取全部规则, 包含停用规则, 空列表由后端返回空数组. */
export function usePrivacyRuleList() {
    return useQuery({
        queryKey: privacyRuleListQueryKey,
        queryFn: () => apiClient.get<PrivacyRule[]>('/api/v1/privacy/list'),
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

export function usePrivacyRuleCreate() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (input: PrivacyRuleCreate) => {
            return apiClient.post<PrivacyRule>('/api/v1/privacy/create', input);
        },
        onSuccess: () => {
            return queryClient.invalidateQueries({ queryKey: privacyRuleListQueryKey });
        },
    });
}

export function usePrivacyRuleUpdate() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (input: PrivacyRuleUpdate) => {
            return apiClient.post<PrivacyRule>('/api/v1/privacy/update', input);
        },
        onSuccess: () => {
            return queryClient.invalidateQueries({ queryKey: privacyRuleListQueryKey });
        },
    });
}

export function usePrivacyRuleDelete() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (id: number): Promise<void> => {
            // 删除成功只返回公共响应字段, 没有规则载荷.
            await apiClient.delete<unknown>(`/api/v1/privacy/delete/${id}`);
        },
        onSuccess: () => {
            return queryClient.invalidateQueries({ queryKey: privacyRuleListQueryKey });
        },
    });
}
