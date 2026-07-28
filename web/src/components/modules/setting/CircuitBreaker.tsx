'use client';

import { useEffect, useState, useRef } from 'react';
import { useTranslations } from 'next-intl';
import { Zap, Hash, Timer, TimerOff, HelpCircle, RotateCcw } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { useSettingList, useSetSetting, SettingKey } from '@/api/endpoints/setting';
import { useCircuitStatus, useResetCircuit, type CircuitBreakerStatus } from '@/api/endpoints/circuit';
import { toast } from '@/components/common/Toast';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';

export function SettingCircuitBreaker() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [enabled, setEnabled] = useState(true);
    const [threshold, setThreshold] = useState('');
    const [cooldown, setCooldown] = useState('');
    const [maxCooldown, setMaxCooldown] = useState('');

    const initialEnabled = useRef(true);
    const initialThreshold = useRef('');
    const initialCooldown = useRef('');
    const initialMaxCooldown = useRef('');

    useEffect(() => {
        if (settings) {
            const en = settings.find(s => s.key === SettingKey.CircuitBreakerEnabled);
            const th = settings.find(s => s.key === SettingKey.CircuitBreakerThreshold);
            const cd = settings.find(s => s.key === SettingKey.CircuitBreakerCooldown);
            const mcd = settings.find(s => s.key === SettingKey.CircuitBreakerMaxCooldown);
            if (en) {
                const v = en.value === 'true';
                queueMicrotask(() => setEnabled(v));
                initialEnabled.current = v;
            }
            if (th) {
                queueMicrotask(() => setThreshold(th.value));
                initialThreshold.current = th.value;
            }
            if (cd) {
                queueMicrotask(() => setCooldown(cd.value));
                initialCooldown.current = cd.value;
            }
            if (mcd) {
                queueMicrotask(() => setMaxCooldown(mcd.value));
                initialMaxCooldown.current = mcd.value;
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.CircuitBreakerEnabled) {
                    initialEnabled.current = value === 'true';
                } else if (key === SettingKey.CircuitBreakerThreshold) {
                    initialThreshold.current = value;
                } else if (key === SettingKey.CircuitBreakerCooldown) {
                    initialCooldown.current = value;
                } else if (key === SettingKey.CircuitBreakerMaxCooldown) {
                    initialMaxCooldown.current = value;
                }
            }
        });
    };

    const handleToggleEnabled = (checked: boolean) => {
        const value = checked ? 'true' : 'false';
        setEnabled(checked);
        handleSave(SettingKey.CircuitBreakerEnabled, value, initialEnabled.current ? 'true' : 'false');
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Zap className="h-5 w-5" />
                {t('circuitBreaker.title')}
                <TooltipProvider>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent>
                            {t('circuitBreaker.hint')}
                        </TooltipContent>
                    </Tooltip>
                </TooltipProvider>
            </h2>

            {/* 启用熔断器开关 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <span className="text-sm font-medium">{t('circuitBreaker.enabled.label')}</span>
                    <TooltipProvider>
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                            </TooltipTrigger>
                            <TooltipContent>
                                {t('circuitBreaker.enabled.hint')}
                            </TooltipContent>
                        </Tooltip>
                    </TooltipProvider>
                </div>
                <Switch
                    checked={enabled}
                    onCheckedChange={handleToggleEnabled}
                />
            </div>

            {/* 熔断触发阈值 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Hash className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.threshold.label')}</span>
                </div>
                <Input
                    type="number"
                    value={threshold}
                    disabled={!enabled}
                    onChange={(e) => setThreshold(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerThreshold, threshold, initialThreshold.current)}
                    placeholder={t('circuitBreaker.threshold.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 基础冷却时间 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Timer className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.cooldown.label')}</span>
                </div>
                <Input
                    type="number"
                    value={cooldown}
                    disabled={!enabled}
                    onChange={(e) => setCooldown(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerCooldown, cooldown, initialCooldown.current)}
                    placeholder={t('circuitBreaker.cooldown.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 最大冷却时间 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <TimerOff className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('circuitBreaker.maxCooldown.label')}</span>
                </div>
                <Input
                    type="number"
                    value={maxCooldown}
                    disabled={!enabled}
                    onChange={(e) => setMaxCooldown(e.target.value)}
                    onBlur={() => handleSave(SettingKey.CircuitBreakerMaxCooldown, maxCooldown, initialMaxCooldown.current)}
                    placeholder={t('circuitBreaker.maxCooldown.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            <CircuitStatusSection />
        </div>
    );
}

function CircuitStatusSection() {
    const t = useTranslations('setting');
    const { data: list, isLoading } = useCircuitStatus();
    const reset = useResetCircuit();

    const items: CircuitBreakerStatus[] = list ?? [];

    const handleReset = () => {
        if (!window.confirm(t('circuitBreaker.status.resetConfirm'))) return;
        reset.mutate(undefined, {
            onSuccess: () => toast.success(t('circuitBreaker.status.reset')),
        });
    };

    return (
        <div className="rounded-2xl border border-border/60 bg-muted/20 p-4 space-y-3">
            <div className="flex items-center justify-between gap-4">
                <h3 className="text-sm font-semibold text-card-foreground">
                    {t('circuitBreaker.status.title')}
                </h3>
                {items.length > 0 && (
                    <Button
                        size="sm"
                        variant="outline"
                        onClick={handleReset}
                        disabled={reset.isPending}
                        className="rounded-xl"
                    >
                        <RotateCcw className="h-4 w-4" />
                        {t('circuitBreaker.status.reset')}
                    </Button>
                )}
            </div>

            {items.length === 0 ? (
                <div className="text-sm text-muted-foreground py-2">
                    {isLoading ? '...' : t('circuitBreaker.status.empty')}
                </div>
            ) : (
                <div className="flex flex-col divide-y divide-border/40 max-h-36 overflow-y-auto">
                    {items.map((item) => (
                        <CircuitStatusRow key={`${item.channel_name}-${item.state}`} item={item} />
                    ))}
                </div>
            )}
        </div>
    );
}

function CircuitStatusRow({ item }: { item: CircuitBreakerStatus }) {
    const t = useTranslations('setting');
    const isOpen = item.state === 'open';
    const cooldownText = isOpen && item.remaining_cooldown > 0
        ? t('circuitBreaker.status.cooldown', { seconds: item.remaining_cooldown })
        : '';

    return (
        <div className="flex items-center justify-between gap-3 py-2.5 text-sm">
            <div className="flex items-center gap-2 min-w-0 flex-1">
                <Badge variant={isOpen ? 'destructive' : 'secondary'} className="shrink-0">
                    {isOpen ? t('circuitBreaker.status.stateOpen') : t('circuitBreaker.status.stateHalfOpen')}
                </Badge>
                <span className="font-mono truncate">
                    {item.channel_name}
                </span>
            </div>
            {cooldownText && (
                <span className="text-muted-foreground shrink-0 tabular-nums">
                    {cooldownText}
                </span>
            )}
        </div>
    );
}
