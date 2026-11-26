'use client';

import { useId, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Loader2, Pencil, Plus, ShieldCheck, Trash2, TriangleAlert } from 'lucide-react';
import {
    PrivacyScope,
    usePrivacyRuleList,
    usePrivacyRuleCreate,
    usePrivacyRuleUpdate,
    usePrivacyRuleDelete,
    type PrivacyFailurePolicy,
    type PrivacyRule,
} from '@/api/endpoints/privacy';
import { SettingKey, useSettingList, useSetSetting } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import {
    AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
    AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog';

const scopeOptions = [
    { bit: PrivacyScope.Messages, key: 'messages' },
    { bit: PrivacyScope.ToolDefinitions, key: 'toolDefinitions' },
    { bit: PrivacyScope.ToolCalls, key: 'toolCalls' },
    { bit: PrivacyScope.ToolResponses, key: 'toolResponses' },
] as const;

type RuleDraft = Omit<PrivacyRule, 'id'> & { id?: number };

function errorMessage(error: unknown, fallback: string): string {
    // apiClient 会抛出普通 ApiError 对象, 不能只判断 Error 实例.
    if (typeof error === 'object' && error !== null && 'message' in error
        && typeof error.message === 'string' && error.message.length > 0) {
        return error.message;
    }
    return fallback;
}

export function SettingPrivacy() {
    const t = useTranslations('setting.privacy');
    const id = useId();
    const settingsQuery = useSettingList();
    const rulesQuery = usePrivacyRuleList();
    const setSetting = useSetSetting();
    const createRule = usePrivacyRuleCreate();
    const updateRule = usePrivacyRuleUpdate();
    const deleteRule = usePrivacyRuleDelete();

    // 仅显式新建或编辑时初始化草稿. 后台查询刷新及设置更新不能覆盖未保存的原文.
    const [draft, setDraft] = useState<RuleDraft | null>(null);
    const [deleteTarget, setDeleteTarget] = useState<PrivacyRule | null>(null);
    const [operationPending, setOperationPending] = useState(false);
    const operationLock = useRef(false);
    const busy = operationPending || setSetting.isPending || createRule.isPending
        || updateRule.isPending || deleteRule.isPending;

    // 设置控件立即保存并直接展示查询数据, 不使用 effect 同步服务端状态或复制配置草稿.
    const enabledValue = settingsQuery.data?.find(s => s.key === SettingKey.PrivacyEnabled)?.value;
    const scopesValue = settingsQuery.data?.find(s => s.key === SettingKey.PrivacyScopes)?.value;
    const policyValue = settingsQuery.data?.find(s => s.key === SettingKey.PrivacyFailurePolicy)?.value;
    const settingsReady = (enabledValue === 'true' || enabledValue === 'false')
        && scopesValue !== undefined && /^\d+$/.test(scopesValue)
        && Number(scopesValue) >= 0 && Number(scopesValue) <= 15
        && (policyValue === 'continue' || policyValue === 'abort');
    const enabled = enabledValue === 'true';
    const scopes = settingsReady ? Number(scopesValue) : PrivacyScope.Messages;
    const policy: PrivacyFailurePolicy = policyValue === 'abort' ? 'abort' : 'continue';
    const rules = rulesQuery.data ?? [];
    const settingsDisabled = busy || !settingsReady || settingsQuery.isError;
    const rulesDisabled = busy || rulesQuery.data === undefined || rulesQuery.isError;

    const source = draft?.source ?? '';
    const alias = draft?.alias ?? '';
    const sourceBytes = new TextEncoder().encode(source);
    // TextEncoder 会替换孤立 surrogate, 用往返校验拒绝静默变更, 并保留合法的开头 BOM.
    const sourceValid = sourceBytes.length >= 1 && sourceBytes.length <= 512
        && new TextDecoder('utf-8', { ignoreBOM: true }).decode(sourceBytes) === source;
    // 严格比较完整 ASCII 别名, 不修正大小写或空白.
    const aliasValid = /^[A-Z0-9_]{1,64}$/.exec(alias)?.[0] === alias;

    async function runMutation(
        mutate: () => Promise<unknown>,
        refresh: () => Promise<{ isError: boolean }>,
        success: string,
        failed: string,
    ): Promise<boolean> {
        // 在 React 渲染禁用状态之前也阻止重复操作.
        if (operationLock.current) return false;
        operationLock.current = true;
        setOperationPending(true);
        try {
            try {
                await mutate();
            } catch (error) {
                toast.error(errorMessage(error, failed));
                return false;
            }
            toast.success(success);
            // 刷新完成前统一禁用操作, 后续完整规则更新和范围位掩码使用最新服务端值.
            try {
                const result = await refresh();
                if (result.isError) toast.warning(t('refreshFailed'));
            } catch {
                // 刷新失败不能被报告为 mutation 失败.
                toast.warning(t('refreshFailed'));
            }
            return true;
        } finally {
            operationLock.current = false;
            setOperationPending(false);
        }
    }

    async function saveSetting(key: string, value: string) {
        if (settingsDisabled) return;
        await runMutation(
            () => setSetting.mutateAsync({ key, value }),
            () => settingsQuery.refetch(),
            t('saved'), t('saveFailed'),
        );
    }

    async function saveRule() {
        if (!draft || busy) return;
        if (!sourceValid) {
            toast.error(t('rules.sourceInvalid'));
            return;
        }
        if (!aliasValid) {
            toast.error(t('rules.aliasInvalid'));
            return;
        }
        const values = { source: draft.source, alias: draft.alias, enabled: draft.enabled };
        const editingId = draft.id;
        const saved = await runMutation(
            () => editingId === undefined
                ? createRule.mutateAsync(values)
                : updateRule.mutateAsync({ id: editingId, ...values }),
            () => rulesQuery.refetch(),
            t(editingId === undefined ? 'rules.created' : 'rules.updated'),
            t(editingId === undefined ? 'rules.createFailed' : 'rules.updateFailed'),
        );
        if (saved) setDraft(null);
    }

    async function toggleRule(rule: PrivacyRule, checked: boolean) {
        if (rulesDisabled) return;
        await runMutation(
            () => updateRule.mutateAsync({
                id: rule.id, source: rule.source, alias: rule.alias, enabled: checked,
            }),
            () => rulesQuery.refetch(),
            t('rules.updated'), t('rules.updateFailed'),
        );
    }

    async function confirmDelete() {
        if (!deleteTarget || busy) return;
        const deleted = await runMutation(
            () => deleteRule.mutateAsync(deleteTarget.id),
            () => rulesQuery.refetch(),
            t('rules.deleted'), t('rules.deleteFailed'),
        );
        if (deleted) setDeleteTarget(null);
    }

    return (
        <section className="rounded-3xl border border-border bg-card p-6 space-y-5" aria-busy={busy}>
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <ShieldCheck className="size-5" />
                {t('title')}
                {busy && <Loader2 className="size-4 animate-spin text-muted-foreground" aria-label={t('pending')} />}
            </h2>
            <p className="text-sm text-muted-foreground">{t('description')}</p>

            {settingsQuery.isPending ? (
                <p className="text-sm text-muted-foreground" role="status">{t('loading')}</p>
            ) : (settingsQuery.isError || !settingsReady) && (
                <div className="space-y-2" role="alert">
                    <p className="text-sm text-destructive">{t('settingsLoadFailed')}</p>
                    <Button variant="outline" size="sm" disabled={busy || settingsQuery.isFetching}
                        onClick={() => void settingsQuery.refetch()}>{t('retry')}</Button>
                </div>
            )}

            <div className="flex items-center justify-between gap-4">
                <Label htmlFor={`${id}-enabled`}>{t('enabled')}</Label>
                <Switch id={`${id}-enabled`} checked={enabled} disabled={settingsDisabled}
                    onCheckedChange={checked => void saveSetting(SettingKey.PrivacyEnabled, String(checked))} />
            </div>

            <fieldset disabled={settingsDisabled} className="space-y-3">
                <legend className="mb-3 text-sm font-semibold">{t('scopes.title')}</legend>
                <p className="text-xs text-muted-foreground">{t('scopes.hint')}</p>
                {scopeOptions.map(option => (
                    <label key={option.bit} className="flex items-start gap-3 rounded-xl border border-border/60 p-3 has-disabled:opacity-60">
                        <input type="checkbox" className="mt-0.5 size-4 shrink-0 accent-primary focus-visible:outline-ring"
                            checked={(scopes & option.bit) !== 0}
                            onChange={event => void saveSetting(SettingKey.PrivacyScopes,
                                String(event.target.checked ? scopes | option.bit : scopes & ~option.bit))} />
                        <span className="min-w-0 space-y-1">
                            <span className="block text-sm font-medium">{t(`scopes.${option.key}.label`)}</span>
                            <span className="block text-xs leading-relaxed text-muted-foreground">{t(`scopes.${option.key}.hint`)}</span>
                        </span>
                    </label>
                ))}
            </fieldset>
            {scopes === 0 && <p className="text-sm text-destructive">{t('scopes.none')}</p>}
            <p className="text-xs leading-relaxed text-destructive">{t('scopes.defaultWarning')}</p>

            <div className="space-y-2">
                <Label htmlFor={`${id}-policy`}>{t('failurePolicy.label')}</Label>
                <Select value={policy} disabled={settingsDisabled} onValueChange={value => {
                    if (value === 'continue' || value === 'abort') {
                        const nextPolicy: PrivacyFailurePolicy = value;
                        void saveSetting(SettingKey.PrivacyFailurePolicy, nextPolicy);
                    }
                }}>
                    <SelectTrigger id={`${id}-policy`} className="w-full rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="continue">{t('failurePolicy.continue')}</SelectItem>
                        <SelectItem value="abort">{t('failurePolicy.abort')}</SelectItem>
                    </SelectContent>
                </Select>
                <p className="text-xs leading-relaxed text-muted-foreground">{t('failurePolicy.continueHint')}</p>
                <p className="text-xs leading-relaxed text-muted-foreground">{t('failurePolicy.abortHint')}</p>
            </div>

            <div className="rounded-2xl border border-destructive/30 bg-destructive/5 p-4 space-y-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold"><TriangleAlert className="size-4 shrink-0" />{t('risks.title')}</h3>
                <ul className="list-disc space-y-2 pl-4 text-xs leading-relaxed text-muted-foreground">
                    <li>{t('risks.exclusions')}</li>
                    <li>{t('risks.metadata')}</li>
                    <li>{t('risks.plaintext')}</li>
                    <li>{t('risks.history')}</li>
                    <li>{t('risks.continue')}</li>
                </ul>
            </div>

            <div className="border-t border-border pt-5 space-y-3">
                <div className="flex items-center justify-between gap-3">
                    <h3 className="text-sm font-semibold">{t('rules.title')}</h3>
                    <Button type="button" size="sm" variant="outline" className="rounded-xl" disabled={rulesDisabled}
                        onClick={() => setDraft({ source: '', alias: '', enabled: true })}>
                        <Plus className="size-4" />{t('rules.add')}
                    </Button>
                </div>
                <p className="text-xs leading-relaxed text-muted-foreground">{t('rules.hint')}</p>
                {rulesQuery.isPending ? (
                    <p className="text-sm text-muted-foreground" role="status">{t('loading')}</p>
                ) : rulesQuery.isError ? (
                    <div className="space-y-2" role="alert">
                        <p className="text-sm text-destructive">{t('rules.loadFailed')}</p>
                        <Button variant="outline" size="sm" disabled={busy || rulesQuery.isFetching}
                            onClick={() => void rulesQuery.refetch()}>{t('retry')}</Button>
                    </div>
                ) : rules.length === 0 ? (
                    <p className="text-sm text-muted-foreground">{t('rules.empty')}</p>
                ) : (
                    <div className="max-h-96 overflow-y-auto space-y-3">
                        {rules.map(rule => (
                            <div key={rule.id} className="rounded-2xl border border-border/60 p-3 space-y-3">
                                <div className="flex items-start justify-between gap-3">
                                    <div className="min-w-0 flex flex-wrap items-center gap-2">
                                        <span className="text-xs text-muted-foreground">#{rule.id}</span>
                                        <code className="break-all text-sm">{rule.alias}</code>
                                        <Badge variant={rule.enabled ? 'secondary' : 'outline'}>{t(rule.enabled ? 'rules.enabled' : 'rules.disabled')}</Badge>
                                    </div>
                                    <Switch checked={rule.enabled} disabled={rulesDisabled}
                                        aria-label={t('rules.toggle', { id: rule.id })}
                                        onCheckedChange={checked => void toggleRule(rule, checked)} />
                                </div>
                                <div className="space-y-1">
                                    <p className="text-xs text-muted-foreground">{t('rules.source')}</p>
                                    <pre className="max-h-32 overflow-y-auto whitespace-pre-wrap break-all rounded-xl bg-muted/40 p-2 text-xs">{rule.source}</pre>
                                </div>
                                <div className="flex justify-end gap-2">
                                    <Button type="button" variant="outline" size="sm" disabled={rulesDisabled}
                                        onClick={() => setDraft({ id: rule.id, source: rule.source, alias: rule.alias, enabled: rule.enabled })}>
                                        <Pencil className="size-3.5" />{t('rules.edit')}
                                    </Button>
                                    <Button type="button" variant="destructive" size="sm" disabled={rulesDisabled}
                                        onClick={() => setDeleteTarget(rule)}>
                                        <Trash2 className="size-3.5" />{t('rules.delete')}
                                    </Button>
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            <Dialog open={draft !== null} onOpenChange={open => { if (!open && !busy) setDraft(null); }}>
                <DialogContent className="max-h-[85dvh] overflow-y-auto rounded-2xl" showCloseButton={false}
                    onInteractOutside={event => event.preventDefault()}>
                    <DialogHeader>
                        <DialogTitle>{t(draft?.id === undefined ? 'rules.createTitle' : 'rules.editTitle')}</DialogTitle>
                        <DialogDescription>{t('rules.formHint')}</DialogDescription>
                    </DialogHeader>
                    {draft && (
                        <form noValidate className="space-y-4" onSubmit={event => { event.preventDefault(); void saveRule(); }}>
                            <div className="space-y-2">
                                <Label htmlFor={`${id}-source`}>{t('rules.source')}</Label>
                                <textarea id={`${id}-source`} rows={5} value={draft.source} disabled={busy} autoFocus
                                    spellCheck={false} autoComplete="off"
                                    aria-invalid={source !== '' && !sourceValid} aria-describedby={`${id}-source-hint`}
                                    className="w-full min-w-0 resize-y rounded-xl border border-input bg-transparent px-3 py-2 font-mono text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive"
                                    onChange={event => setDraft({ ...draft, source: event.target.value })} />
                                <p id={`${id}-source-hint`} className="text-xs text-muted-foreground">{t('rules.sourceHint', { bytes: sourceBytes.length })}</p>
                                {source !== '' && !sourceValid && <p className="text-xs text-destructive">{t('rules.sourceInvalid')}</p>}
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor={`${id}-alias`}>{t('rules.alias')}</Label>
                                <Input id={`${id}-alias`} value={draft.alias} disabled={busy} className="rounded-xl font-mono"
                                    autoComplete="off" autoCapitalize="none" spellCheck={false}
                                    aria-invalid={alias !== '' && !aliasValid} aria-describedby={`${id}-alias-hint`}
                                    onChange={event => setDraft({ ...draft, alias: event.target.value })} />
                                <p id={`${id}-alias-hint`} className="text-xs text-muted-foreground">{t('rules.aliasHint')}</p>
                                {alias !== '' && !aliasValid && <p className="text-xs text-destructive">{t('rules.aliasInvalid')}</p>}
                                {aliasValid && <code className="block break-all text-xs text-muted-foreground">{`[[OCTOPUS_PII:${draft.alias}]]`}</code>}
                            </div>
                            <div className="flex items-center justify-between gap-4">
                                <Label htmlFor={`${id}-rule-enabled`}>{t('rules.enabled')}</Label>
                                <Switch id={`${id}-rule-enabled`} checked={draft.enabled} disabled={busy}
                                    onCheckedChange={checked => setDraft({ ...draft, enabled: checked })} />
                            </div>
                            {draft.id !== undefined && <p className="text-xs leading-relaxed text-destructive">{t('risks.history')}</p>}
                            <DialogFooter>
                                <Button type="button" variant="outline" disabled={busy} onClick={() => setDraft(null)}>{t('rules.cancel')}</Button>
                                <Button type="submit" disabled={busy || !sourceValid || !aliasValid}>
                                    {busy && <Loader2 className="size-4 animate-spin" />}
                                    {t(draft.id === undefined ? 'rules.create' : 'rules.save')}
                                </Button>
                            </DialogFooter>
                        </form>
                    )}
                </DialogContent>
            </Dialog>

            <AlertDialog open={deleteTarget !== null} onOpenChange={open => { if (!open && !busy) setDeleteTarget(null); }}>
                <AlertDialogContent className="rounded-2xl">
                    <AlertDialogHeader>
                        <AlertDialogTitle>{t('rules.deleteTitle')}</AlertDialogTitle>
                        <AlertDialogDescription>{t('rules.deleteWarning')}</AlertDialogDescription>
                    </AlertDialogHeader>
                    {deleteTarget && <code className="break-all text-sm">#{deleteTarget.id} · {deleteTarget.alias}</code>}
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={busy}>{t('rules.cancel')}</AlertDialogCancel>
                        <AlertDialogAction disabled={busy} className="bg-destructive text-white hover:bg-destructive/90"
                            onClick={event => { event.preventDefault(); void confirmDelete(); }}>
                            {busy && <Loader2 className="size-4 animate-spin" />}{t('rules.confirmDelete')}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </section>
    );
}
