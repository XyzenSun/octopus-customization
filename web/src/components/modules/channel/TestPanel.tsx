import { useMemo, useState } from 'react';
import { Play, ChevronDown } from 'lucide-react';
import { useTranslations } from 'next-intl';
import {
    useTestChannelModel,
    type Channel,
    type CreateChannelRequest,
    type TestModelResult,
} from '@/api/endpoints/channel';
import type { ChannelFormData } from './Form';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

type TestStatus = 'idle' | 'testing' | 'ok' | 'client_error' | 'server_error' | 'network_error';

interface ModelTestState {
    status: TestStatus;
    delay_ms?: number;
    status_code?: number;
    error?: string;
}

interface TestPanelProps {
    /**
     * 已保存渠道时传完整 Channel,id 字段必填。
     * 新建/编辑场景传 formData(还未保存)。
     * 二选一;两个都传时优先用 channel。
     */
    channel?: Channel;
    formData?: ChannelFormData;
}

/*
 * 渠道编辑弹窗右侧的"模型测试"窄栏。
 * 单行展示每个已选模型,通过状态点 + HTTP 状态码 chip 反馈最近测试结果。
 * 支持单独测试某行 / 勾选后批量测试(逐个串行)。
 *
 * 状态点颜色:
 *   200 -> emerald;  4xx -> red;  5xx -> amber;
 *   0 (网络错/超时) -> red;  未测 -> gray;  测试中 -> amber pulse。
 */
export function TestPanel({ channel, formData }: TestPanelProps) {
    const t = useTranslations('channel.test');
    const testModel = useTestChannelModel();

    // 输入参数(仅当前弹窗内有效,关闭即丢)
    const [timeoutSec, setTimeoutSec] = useState<number>(30);
    const [keyIndex, setKeyIndex] = useState<number>(0);
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [results, setResults] = useState<Map<string, ModelTestState>>(new Map());
    const [isBatch, setIsBatch] = useState(false);

    // 模型集合与统计:model + custom_model 合并去重
    const modelStr = channel ? channel.model : (formData?.model ?? '');
    const customModelStr = channel ? channel.custom_model : (formData?.custom_model ?? '');
    const modelInfo = useMemo(() => {
        const split = (s?: string) =>
            (s ?? '').split(',').map((x) => x.trim()).filter(Boolean);
        const fromSync = split(modelStr);
        const fromCustom = split(customModelStr);
        const all = Array.from(new Set([...fromSync, ...fromCustom]));
        return { syncCount: fromSync.length, customCount: fromCustom.length, all };
    }, [modelStr, customModelStr]);

    // 当前类型的 keys 数组;新建场景下用 formData.keys(可能没有 id)
    const formKeys = formData?.keys;
    const channelKeys = channel?.keys;
    const keys = useMemo(() => {
        if (channelKeys) return channelKeys;
        return (formKeys ?? []) as Array<{
            id?: number;
            channel_key: string;
            remark?: string;
            enabled?: boolean;
        }>;
    }, [channelKeys, formKeys]);

    const channelType = channel?.type ?? formData?.type;
    const isEmbedding = channelType === 'openai/embeddings';

    // 没有 keys 时不能测
    const hasValidKey = keys.some((k) => k.channel_key && k.channel_key.trim());
    // 选定的 key 下标合法性
    const effectiveKeyIndex = Math.min(Math.max(0, keyIndex), Math.max(0, keys.length - 1));

    const buildRequest = (model: string) => {
        const base = {
            model,
            key_index: effectiveKeyIndex,
            timeout_ms: timeoutSec * 1000,
        };
        if (channel) {
            return { ...base, channel_id: channel.id };
        }
        // 新建/编辑场景:把 formData 转成 create 请求风格 (note:这是有意传明文 - 见后端注释)
        if (formData) {
            const tempChannel: CreateChannelRequest = {
                name: formData.name,
                type: formData.type,
                enabled: formData.enabled,
                base_urls: (formData.base_urls ?? []).filter((u) => u.url.trim()).map((u) => ({
                    url: u.url.trim(),
                    delay: Number(u.delay || 0),
                })),
                keys: (formData.keys ?? []).filter((k) => k.channel_key?.trim()).map((k) => ({
                    enabled: k.enabled ?? true,
                    channel_key: k.channel_key!,
                    remark: k.remark ?? '',
                })),
                model: formData.model,
                custom_model: formData.custom_model,
                proxy: formData.proxy ?? false,
                auto_sync: formData.auto_sync ?? false,
                auto_group: formData.auto_group,
                custom_header: (formData.custom_header ?? [])
                    .map((h) => ({ header_key: h.header_key.trim(), header_value: h.header_value }))
                    .filter((h) => h.header_key && h.header_value !== ''),
                channel_proxy: formData.channel_proxy ?? null,
                param_override: formData.param_override ?? null,
                match_regex: formData.match_regex ?? null,
            };
            return { ...base, channel: tempChannel as unknown as Channel };
        }
        return base;
    };

    const updateResult = (model: string, next: ModelTestState) => {
        setResults((prev) => {
            const m = new Map(prev);
            m.set(model, next);
            return m;
        });
    };

    const toState = (r: TestModelResult): ModelTestState => {
        let status: TestStatus;
        if (r.status_code === 200) status = 'ok';
        else if (r.status_code === 0) status = 'network_error';
        else if (r.status_code >= 500) status = 'server_error';
        else if (r.status_code >= 400) status = 'client_error';
        else status = 'client_error'; // 2xx 中非 200 / 3xx 重定向,视为客户端错
        return { status, delay_ms: r.delay_ms, status_code: r.status_code, error: r.error };
    };

    const runOne = async (model: string) => {
        const req = buildRequest(model);
        updateResult(model, { status: 'testing' });
        try {
            const r = await testModel.mutateAsync(req as Parameters<typeof testModel.mutateAsync>[0]);
            updateResult(model, toState(r));
        } catch (e) {
            // HTTP 层错误(后端 4xx/5xx 等报错)走这里
            updateResult(model, {
                status: 'client_error',
                error: (e as { message?: string })?.message ?? 'request failed',
            });
        }
    };

    const runBatch = async () => {
        if (isBatch || selected.size === 0) return;
        setIsBatch(true);
        try {
            // 后端串行测,前端逐个发,这样 UI 进度可见
            for (const model of selected) {
                await runOne(model);
            }
        } finally {
            setIsBatch(false);
        }
    };

    const toggleSelect = (model: string) => {
        setSelected((prev) => {
            const next = new Set(prev);
            if (next.has(model)) next.delete(model);
            else next.add(model);
            return next;
        });
    };

    const toggleSelectAll = () => {
        setSelected((prev) => (prev.size === modelInfo.all.length ? new Set() : new Set(modelInfo.all)));
    };

    // 渲染辅助
    const renderDot = (state?: ModelTestState) => {
        if (!state || state.status === 'idle') {
            return <span className="inline-block size-2 rounded-full bg-gray-300" />;
        }
        if (state.status === 'testing') {
            return <span className="inline-block size-2 rounded-full bg-amber-500 animate-pulse" />;
        }
        if (state.status === 'ok') {
            return <span className="inline-block size-2 rounded-full bg-emerald-500" />;
        }
        if (state.status === 'server_error') {
            return <span className="inline-block size-2 rounded-full bg-amber-500" />;
        }
        // client_error / network_error
        return <span className="inline-block size-2 rounded-full bg-red-500" />;
    };

    const renderStatusChip = (state?: ModelTestState) => {
        if (!state || state.status === 'idle' || state.status === 'testing') return null;
        if (state.status === 'network_error') {
            return (
                <Badge variant="secondary" className="h-5 px-1.5 text-[10px] font-mono bg-red-500/15 text-red-700 dark:text-red-400">
                    {t('statusNetwork')}
                </Badge>
            );
        }
        const cls = state.status === 'ok'
            ? 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-400'
            : state.status === 'server_error'
                ? 'bg-amber-500/15 text-amber-700 dark:text-amber-400'
                : 'bg-red-500/15 text-red-700 dark:text-red-400';
        return (
            <Badge variant="secondary" className={cn('h-5 px-1.5 text-[10px] font-mono', cls)} title={state.error}>
                {state.status_code}
            </Badge>
        );
    };

    const renderDelay = (state?: ModelTestState) => {
        if (!state || state.status === 'idle') return <span className="text-[10px] text-muted-foreground">{t('statusUntested')}</span>;
        if (state.status === 'testing') return <span className="text-[10px] text-muted-foreground">…</span>;
        if (typeof state.delay_ms !== 'number') return null;
        return (
            <span className="text-[10px] font-mono text-muted-foreground">
                {state.delay_ms < 1000 ? `${state.delay_ms}ms` : `${(state.delay_ms / 1000).toFixed(1)}s`}
            </span>
        );
    };

    return (
        <aside className="w-64 shrink-0 border-l border-border bg-card/50 px-4 py-4 hidden sm:flex flex-col gap-3 overflow-y-auto">
            {/* 标题 + 超时输入 */}
            <header className="flex items-end justify-between gap-2 pb-2 border-b border-border">
                <h3 className="text-sm font-semibold text-card-foreground">{t('title')}</h3>
                <label className="flex items-center gap-1 text-xs text-muted-foreground">
                    {t('timeoutLabel')}
                    <input
                        type="number"
                        min={1}
                        max={300}
                        value={timeoutSec}
                        onChange={(e) => setTimeoutSec(Math.max(1, Math.min(300, Number(e.target.value) || 30)))}
                        className="w-12 px-1 py-0.5 text-xs bg-background border border-border rounded-md text-right"
                        disabled={isEmbedding}
                    />
                    <span>s</span>
                </label>
            </header>

            {/* 模型数量统计 */}
            <div className="text-xs text-muted-foreground">
                {t('modelCount', { total: modelInfo.all.length, sync: modelInfo.syncCount, custom: modelInfo.customCount })}
            </div>

            {/* Key 选择下拉 */}
            <div className="text-xs space-y-1">
                <div className="text-muted-foreground">{t('keyLabel')}</div>
                <div className="relative">
                    <select
                        value={effectiveKeyIndex}
                        onChange={(e) => setKeyIndex(Number(e.target.value))}
                        disabled={isEmbedding || keys.length === 0}
                        className="w-full appearance-none bg-background border border-border rounded-md px-2 py-1 text-xs font-mono pr-6 truncate"
                    >
                        {keys.length === 0 && <option value={0}>{t('noKeys')}</option>}
                        {keys.map((k, idx) => {
                            const label = k.remark?.trim()
                                ? truncate(k.remark.trim(), 10)
                                : truncate((k.channel_key ?? '').trim(), 10);
                            return <option key={idx} value={idx}>{label || `#${idx}`}</option>;
                        })}
                    </select>
                    <ChevronDown className="absolute right-1.5 top-1/2 -translate-y-1/2 size-3 pointer-events-none text-muted-foreground" />
                </div>
            </div>

            {/* 模型列表 */}
            <div className="flex-1 space-y-1 min-h-0 overflow-y-auto">
                {modelInfo.all.length === 0 ? (
                    <div className="text-xs text-muted-foreground text-center py-6">{t('noModels')}</div>
                ) : (
                    modelInfo.all.map((m) => {
                        const state = results.get(m);
                        const checked = selected.has(m);
                        return (
                            <div
                                key={m}
                                className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-background/50 border border-border/50 hover:bg-background transition-colors group"
                            >
                                <input
                                    type="checkbox"
                                    checked={checked}
                                    onChange={() => toggleSelect(m)}
                                    disabled={isEmbedding}
                                    className="size-3.5 accent-primary cursor-pointer shrink-0"
                                />
                                <span
                                    className="flex-1 min-w-0 truncate text-xs font-mono text-card-foreground"
                                    title={state?.error || m}
                                >
                                    {m}
                                </span>
                                <button
                                    type="button"
                                    onClick={() => runOne(m)}
                                    disabled={isEmbedding || !hasValidKey || state?.status === 'testing'}
                                    className="p-1 rounded-md hover:bg-accent disabled:opacity-40 disabled:cursor-not-allowed shrink-0 transition-colors"
                                    title={t('testButtonTitle')}
                                >
                                    <Play className="size-3 text-foreground" />
                                </button>
                                {renderDot(state)}
                                {renderDelay(state)}
                                {renderStatusChip(state)}
                            </div>
                        );
                    })
                )}
            </div>

            {/* 底部批量操作 */}
            <footer className="flex items-center justify-between gap-2 pt-2 border-t border-border text-xs">
                <span className="text-muted-foreground">
                    {t('selectedCount', { selected: selected.size, total: modelInfo.all.length })}
                </span>
                <div className="flex items-center gap-1">
                    <button
                        type="button"
                        onClick={toggleSelectAll}
                        disabled={isEmbedding || modelInfo.all.length === 0}
                        className="px-2 py-1 rounded-md border border-border bg-background hover:bg-accent disabled:opacity-40 transition-colors"
                    >
                        {selected.size === modelInfo.all.length ? t('deselectAll') : t('selectAll')}
                    </button>
                    <button
                        type="button"
                        onClick={runBatch}
                        disabled={isEmbedding || !hasValidKey || isBatch || selected.size === 0}
                        className="px-2 py-1 rounded-md bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-40 transition-colors"
                    >
                        {isBatch ? t('testing') : t('batchTest')}
                    </button>
                </div>
            </footer>
        </aside>
    );
}

function truncate(s: string, n: number): string {
    if (s.length <= n) return s;
    return s.slice(0, n) + '…';
}
