import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient, API_BASE_URL } from '../client';
import { logger } from '@/lib/logger';
import { useAuthStore } from './user';
import type { APIKey } from './apikey';
import type { BaseUrl, Channel, ChannelKey, CustomHeader } from './channel';
import type { Group, GroupItem } from './group';
import type { ChannelAttempt, LegacyRelayLog } from './log';
import type { LLMInfo } from './model';
import { privacyRuleListQueryKey, type PrivacyRule } from './privacy';
import type { StatsAPIKey, StatsChannel, StatsDaily, StatsHourly, StatsTotal } from './stats';

/**
 * Setting 数据
 */
export interface Setting {
    key: string;
    value: string;
}

export const SettingKey = {
    ProxyURL: 'proxy_url',
    StatsSaveInterval: 'stats_save_interval',
    ModelInfoUpdateInterval: 'model_info_update_interval',
    SyncLLMInterval: 'sync_llm_interval',
    RelayLogKeepEnabled: 'relay_log_keep_enabled',
    RelayLogKeepPeriod: 'relay_log_keep_period',
    RelayLogFlushSize: 'relay_log_flush_size',
    RelayLogMemoryCacheSize: 'relay_log_memory_cache_size',
    RelayLogMaxContentSizeMB: 'relay_log_max_content_size_mb',
    RelayLogMemoryLogMaxDimidiateTimes: 'relay_log_memory_log_max_dimidiate_times',
    CORSAllowOrigins: 'cors_allow_origins',
    CircuitBreakerThreshold: 'circuit_breaker_threshold',
    CircuitBreakerCooldown: 'circuit_breaker_cooldown',
    CircuitBreakerMaxCooldown: 'circuit_breaker_max_cooldown',
    CircuitBreakerEnabled: 'circuit_breaker_enabled',
    KeyCircuitBreakerEnabled: 'key_circuit_breaker_enabled',
    PassthroughEnabled: 'passthrough_enabled',
    PrivacyEnabled: 'privacy_enabled',
    PrivacyScopes: 'privacy_scopes',
    PrivacyFailurePolicy: 'privacy_failure_policy',
} as const;

/**
 * 获取 Setting 列表 Hook
 * 
 * @example
 * const { data: settings, isLoading, error } = useSettingList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * // settings 的值只用于展示，不写入日志。
 */
export function useSettingList() {
    return useQuery({
        queryKey: ['settings', 'list'],
        queryFn: async () => {
            return apiClient.get<Setting[]>('/api/v1/setting/list');
        },
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

/**
 * 设置 Setting Hook
 * 
 * @example
 * const setSetting = useSetSetting();
 * 
 * setSetting.mutate({
 *   key: 'theme',
 *   value: 'dark',
 * });
 */
export function useSetSetting() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async (data: Setting) => {
            return apiClient.post<Setting>('/api/v1/setting/set', data);
        },
        onSuccess: () => {
            logger.log('Setting 设置成功');
            queryClient.invalidateQueries({ queryKey: ['settings', 'list'] });
        },
        onError: () => {
            logger.error('Setting 设置失败');
        },
    });
}

/**
 * 备份保留后端原始结构，不使用渠道列表中归一化后的数组和统计数据。
 */
type DBDumpChannel = Omit<Channel,
    'type' | 'auto_group' | 'base_urls' | 'keys' | 'custom_header' | 'stats'
    | 'param_override' | 'channel_proxy' | 'match_regex'
> & {
    type: string;
    auto_group: number;
    base_urls: BaseUrl[] | null;
    keys: ChannelKey[] | null;
    custom_header: CustomHeader[] | null;
    param_override: string | null;
    channel_proxy: string | null;
    match_regex: string | null;
    stats?: StatsChannel;
};

type DBDumpGroup = Omit<Group, 'id' | 'mode' | 'items'> & {
    id: number;
    mode: number;
    items?: Required<GroupItem>[];
};

/** 当前备份版本仍为 1，兼容版本 0/1 的旧备份缺少隐私规则数组。 */
export interface DBDump {
    version: 0 | 1;
    exported_at: string;
    include_logs: boolean;
    include_stats: boolean;
    channels?: DBDumpChannel[];
    channel_keys?: ChannelKey[];
    groups?: DBDumpGroup[];
    group_items?: Required<GroupItem>[];
    llm_infos?: LLMInfo[];
    api_keys?: APIKey[];
    settings?: Setting[];
    privacy_rules?: PrivacyRule[];
    // StatsTotal 的后端 JSON 字段为大写 ID，不沿用前端查询类型的小写 id。
    stats_total?: (Omit<StatsTotal, 'id'> & { ID: number })[];
    stats_daily?: StatsDaily[];
    stats_hourly?: StatsHourly[];
    stats_model?: (StatsChannel & { id: number; name: string })[];
    stats_channel?: StatsChannel[];
    stats_api_key?: StatsAPIKey[];
    relay_logs?: (Omit<LegacyRelayLog, 'attempts'> & { attempts?: ChannelAttempt[] | null })[];
}

/**
 * 数据库导入/导出
 */
export interface DBImportResult {
    rows_affected: Record<string, number>;
}

export interface DBExportOptions {
    include_logs?: boolean;
    include_stats?: boolean;
}

type ApiResponse<T> = {
    code?: number;
    message?: string;
    data?: T;
};

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null;
}

function getMessageField(value: unknown): string | undefined {
    if (!isRecord(value)) return undefined;
    const msg = value.message;
    return typeof msg === 'string' ? msg : undefined;
}

function getDataField<T>(value: unknown): T | undefined {
    if (!isRecord(value)) return undefined;
    return (value as ApiResponse<T>).data;
}

function getAuthHeader(): string {
    const token = useAuthStore.getState().token;
    if (!token) throw new Error('Not authenticated');
    return `Bearer ${token}`;
}

function parseFilename(contentDisposition: string | null): string | null {
    if (!contentDisposition) return null;
    // e.g. attachment; filename="octopus-export-20250101120000.json"
    const match = contentDisposition.match(/filename="([^"]+)"/i);
    return match?.[1] ?? null;
}

function exportFallbackFilename() {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
    return `octopus-export-${ts}.json`;
}

async function downloadBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

/**
 * 导出数据库（下载 JSON 文件）
 */
export function useExportDB() {
    return useMutation({
        mutationFn: async (options: DBExportOptions = {}) => {
            const params = new URLSearchParams();
            params.set('include_logs', String(!!options.include_logs));
            params.set('include_stats', String(!!options.include_stats));

            const res = await fetch(`${API_BASE_URL}/api/v1/setting/export?${params.toString()}`, {
                method: 'GET',
                headers: {
                    Authorization: getAuthHeader(),
                },
            });

            if (!res.ok) {
                const text = await res.text();
                throw new Error(text || res.statusText);
            }

            const blob = await res.blob();
            const filename = parseFilename(res.headers.get('content-disposition')) || exportFallbackFilename();
            await downloadBlob(blob, filename);
            return { filename };
        },
        onError: () => {
            // 备份错误可能包含明文，日志只记录固定提示。
            logger.error('导出数据库失败');
        },
    });
}

/**
 * 导入数据库（上传 JSON 文件，增量导入）
 */
export function useImportDB() {
    const queryClient = useQueryClient();
    const refreshPrivacyCaches = () => {
        // 重置会清除旧值并取消旧查询，刷新失败时也不会继续展示旧规则或设置。
        // 活跃查询立即刷新，未挂载的查询下次使用时重新获取。
        return Promise.all([
            queryClient.resetQueries({ queryKey: privacyRuleListQueryKey }),
            queryClient.resetQueries({ queryKey: ['settings', 'list'] }),
        ]);
    };

    return useMutation({
        mutationFn: async (file: File) => {
            const form = new FormData();
            form.append('file', file);

            const res = await fetch(`${API_BASE_URL}/api/v1/setting/import`, {
                method: 'POST',
                headers: {
                    Authorization: getAuthHeader(),
                },
                body: form,
            });

            const contentType = res.headers.get('content-type') || '';
            const isJson = contentType.includes('application/json');
            const data = isJson ? await res.json() : await res.text();

            if (!res.ok) {
                const message = getMessageField(data) ?? (typeof data === 'string' ? data : res.statusText);
                throw new Error(message);
            }

            // 支持后端标准 ApiResponse：{code,message,data:{...}}
            const nested = getDataField<DBImportResult>(data);
            return nested ?? (data as DBImportResult);
        },
        onSuccess: refreshPrivacyCaches,
        onError: async (error) => {
            // 此固定错误表示数据库已经提交，不能按回滚处理或保留旧规则。
            if (error.message === 'backup import committed but cache refresh failed') {
                await refreshPrivacyCaches();
            }
            logger.error('导入数据库失败');
        },
    });
}

