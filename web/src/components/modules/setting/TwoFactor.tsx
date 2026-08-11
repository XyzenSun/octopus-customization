'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { ShieldCheck, ShieldOff } from 'lucide-react';
import Image from 'next/image';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { CopyIconButton } from '@/components/common/CopyButton';
import { toast } from '@/components/common/Toast';
import {
    useServerTime,
    useSetupTwoFactor,
    useEnableTwoFactor,
    useDisableTwoFactor,
    type TwoFactorSetupResponse,
} from '@/api/endpoints/user';

/**
 * 两步验证（TOTP）管理区块。
 *
 * 绑定分两步：先 setup 拿密钥并扫码，再输入一次验证码才真正启用。
 * 这样扫码失败时最坏结果只是没绑成功，不会出现"已开启却算不出正确验证码"
 * 导致账号锁死——单用户系统没有第二个管理员能重置。
 */
export function TwoFactorSection({ onEnabled }: { onEnabled: () => void }) {
    const t = useTranslations('setting');

    const serverTimeQuery = useServerTime();
    const setupMutation = useSetupTwoFactor();
    const enableMutation = useEnableTwoFactor();
    const disableMutation = useDisableTwoFactor();

    const [setupData, setSetupData] = useState<TwoFactorSetupResponse | null>(null);
    const [enableCode, setEnableCode] = useState('');
    const [disableDialogOpen, setDisableDialogOpen] = useState(false);
    const [disableCode, setDisableCode] = useState('');

    const enabled = serverTimeQuery.data?.two_factor_enabled ?? false;

    const handleSetup = () => {
        setEnableCode('');
        setupMutation.mutate(undefined, {
            onSuccess: (data) => setSetupData(data),
            onError: () => toast.error(t('account.twoFactor.setupFailed')),
        });
    };

    const handleEnable = () => {
        enableMutation.mutate(enableCode, {
            onSuccess: () => {
                setSetupData(null);
                setEnableCode('');
                toast.success(t('account.twoFactor.enableSuccess'));
                // 已签发的 token 是纯密码换来的，不受两步验证保护；
                // 与修改密码/用户名一致，启用后要求重新登录一次。
                setTimeout(onEnabled, 1000);
            },
            onError: () => toast.error(t('account.twoFactor.codeInvalid')),
        });
    };

    const handleDisable = () => {
        disableMutation.mutate(disableCode, {
            onSuccess: () => {
                setDisableDialogOpen(false);
                setDisableCode('');
                toast.success(t('account.twoFactor.disableSuccess'));
                serverTimeQuery.refetch();
            },
            onError: () => toast.error(t('account.twoFactor.codeInvalid')),
        });
    };

    return (
        <div className="space-y-3">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                <ShieldCheck className="size-4" />
                {t('account.twoFactor.label')}
            </div>

            <div className="flex items-center justify-between gap-4">
                <p className="text-sm text-muted-foreground">
                    {enabled ? t('account.twoFactor.statusEnabled') : t('account.twoFactor.statusDisabled')}
                </p>
                {enabled ? (
                    <Button
                        variant="outline"
                        onClick={() => setDisableDialogOpen(true)}
                        className="rounded-xl"
                    >
                        <ShieldOff className="size-4" />
                        {t('account.twoFactor.disable')}
                    </Button>
                ) : (
                    <Button
                        onClick={handleSetup}
                        disabled={setupMutation.isPending}
                        className="rounded-xl"
                    >
                        <ShieldCheck className="size-4" />
                        {setupMutation.isPending ? t('account.saving') : t('account.twoFactor.enable')}
                    </Button>
                )}
            </div>

            {/* 绑定流程：扫码 → 输验证码 → 启用 */}
            <Dialog open={setupData !== null} onOpenChange={(open) => !open && setSetupData(null)}>
                <DialogContent className="sm:max-w-md">
                    <DialogHeader>
                        <DialogTitle>{t('account.twoFactor.setupTitle')}</DialogTitle>
                        <DialogDescription>{t('account.twoFactor.setupDescription')}</DialogDescription>
                    </DialogHeader>

                    {setupData && (
                        <div className="space-y-4">
                            <div className="flex justify-center">
                                <Image
                                    src={setupData.qr_code}
                                    alt={t('account.twoFactor.qrAlt')}
                                    width={200}
                                    height={200}
                                    unoptimized
                                    className="rounded-xl border border-border bg-white p-2"
                                />
                            </div>

                            {/* 部分环境（远程终端、截图受限）扫不了码，提供密钥明文手动录入 */}
                            <div className="space-y-2">
                                <p className="text-sm text-muted-foreground">
                                    {t('account.twoFactor.manualEntry')}
                                </p>
                                <div className="flex items-center gap-2 rounded-xl border border-border bg-muted/50 px-3 py-2">
                                    <code className="flex-1 break-all font-mono text-xs">{setupData.secret}</code>
                                    <CopyIconButton text={setupData.secret} />
                                </div>
                            </div>

                            <Input
                                type="text"
                                inputMode="numeric"
                                autoComplete="one-time-code"
                                maxLength={6}
                                value={enableCode}
                                onChange={(e) => setEnableCode(e.target.value.replace(/\D/g, ''))}
                                placeholder={t('account.twoFactor.codePlaceholder')}
                                className="rounded-xl text-center tracking-[0.3em]"
                            />
                        </div>
                    )}

                    <DialogFooter>
                        <Button variant="outline" onClick={() => setSetupData(null)} className="rounded-xl">
                            {t('account.twoFactor.cancel')}
                        </Button>
                        <Button
                            onClick={handleEnable}
                            disabled={enableMutation.isPending || enableCode.length !== 6}
                            className="rounded-xl"
                        >
                            {enableMutation.isPending ? t('account.saving') : t('account.twoFactor.confirm')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* 关闭同样要求验证码：仅凭已登录状态就能一键关掉的话，这层防护形同虚设 */}
            <Dialog open={disableDialogOpen} onOpenChange={setDisableDialogOpen}>
                <DialogContent className="sm:max-w-md">
                    <DialogHeader>
                        <DialogTitle>{t('account.twoFactor.disableTitle')}</DialogTitle>
                        <DialogDescription>{t('account.twoFactor.disableDescription')}</DialogDescription>
                    </DialogHeader>

                    <Input
                        type="text"
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        maxLength={6}
                        value={disableCode}
                        onChange={(e) => setDisableCode(e.target.value.replace(/\D/g, ''))}
                        placeholder={t('account.twoFactor.codePlaceholder')}
                        className="rounded-xl text-center tracking-[0.3em]"
                    />

                    <DialogFooter>
                        <Button
                            variant="outline"
                            onClick={() => setDisableDialogOpen(false)}
                            className="rounded-xl"
                        >
                            {t('account.twoFactor.cancel')}
                        </Button>
                        <Button
                            variant="destructive"
                            onClick={handleDisable}
                            disabled={disableMutation.isPending || disableCode.length !== 6}
                            className="rounded-xl"
                        >
                            {disableMutation.isPending ? t('account.saving') : t('account.twoFactor.disable')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
