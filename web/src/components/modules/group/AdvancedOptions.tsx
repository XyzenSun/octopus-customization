'use client';

import { useState } from 'react';
import { Plus, X } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { CustomHeader } from '@/api/endpoints/channel';
import { Button } from '@/components/ui/button';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

export type GroupRequestOptions = {
    custom_header: CustomHeader[];
    param_override: string;
};

function editableHeaders(headers: CustomHeader[]) {
    return headers.length > 0
        ? headers.map((header) => ({ ...header }))
        : [{ header_key: '', header_value: '' }];
}

export function normalizeGroupRequestOptions(options: GroupRequestOptions): GroupRequestOptions {
    return {
        custom_header: options.custom_header
            .map((header) => ({
                header_key: header.header_key.trim(),
                header_value: header.header_value,
            }))
            .filter((header) => header.header_key && header.header_value !== ''),
        param_override: options.param_override.trim(),
    };
}

export function GroupAdvancedOptionsDialog({
    value,
    onSave,
    isSaving = false,
    triggerClassName,
}: {
    value: GroupRequestOptions;
    onSave: (value: GroupRequestOptions, onDone: () => void) => void;
    isSaving?: boolean;
    triggerClassName?: string;
}) {
    const t = useTranslations('group');

    return (
        <MorphingDialog>
            <MorphingDialogTrigger
                className={cn(
                    'flex items-center justify-center py-1 text-center text-xs rounded-lg bg-muted hover:bg-muted/80 transition-colors',
                    triggerClassName
                )}
            >
                <span>{t('advanced.title')}</span>
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-screen max-w-full sm:max-w-xl max-h-[calc(100vh-2rem)] overflow-y-auto rounded-3xl bg-card p-6 text-card-foreground">
                    <AdvancedOptionsContent
                        value={value}
                        onSave={onSave}
                        isSaving={isSaving}
                    />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

function AdvancedOptionsContent({
    value,
    onSave,
    isSaving,
}: {
    value: GroupRequestOptions;
    onSave: (value: GroupRequestOptions, onDone: () => void) => void;
    isSaving: boolean;
}) {
    const t = useTranslations('group');
    const { setIsOpen } = useMorphingDialog();
    const [draft, setDraft] = useState<GroupRequestOptions>(() => ({
        custom_header: editableHeaders(value.custom_header),
        param_override: value.param_override,
    }));

    const updateHeader = (index: number, patch: Partial<CustomHeader>) => {
        setDraft((current) => ({
            ...current,
            custom_header: current.custom_header.map((header, currentIndex) => (
                currentIndex === index ? { ...header, ...patch } : header
            )),
        }));
    };

    const removeHeader = (index: number) => {
        setDraft((current) => {
            if (current.custom_header.length <= 1) {
                return { ...current, custom_header: [{ header_key: '', header_value: '' }] };
            }
            return {
                ...current,
                custom_header: current.custom_header.filter((_, currentIndex) => currentIndex !== index),
            };
        });
    };

    const closeDialog = () => setIsOpen(false);

    return (
        <>
            <MorphingDialogTitle>
                <header className="mb-5 flex items-start justify-between gap-4">
                    <div className="space-y-2">
                        <h2 className="text-2xl font-bold text-card-foreground">
                            {t('advanced.title')}
                        </h2>
                        <p className="text-sm text-muted-foreground">
                            {t('advanced.description')}
                        </p>
                    </div>
                    <MorphingDialogClose className="relative right-0 top-0" />
                </header>
            </MorphingDialogTitle>

            <MorphingDialogDescription disableLayoutAnimation className="space-y-5">
                <div className="space-y-2">
                    <div className="flex items-center justify-between">
                        <label className="text-sm font-medium text-card-foreground">
                            {t('advanced.customHeader')}
                        </label>
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => setDraft((current) => ({
                                ...current,
                                custom_header: [
                                    ...current.custom_header,
                                    { header_key: '', header_value: '' },
                                ],
                            }))}
                            className="h-6 px-2 text-xs text-muted-foreground/70 hover:bg-transparent hover:text-muted-foreground"
                        >
                            <Plus className="mr-1 size-3" />
                            {t('advanced.addHeader')}
                        </Button>
                    </div>

                    <div className="space-y-2">
                        {draft.custom_header.map((header, index) => (
                            <div key={`group-header-${index}`} className="flex items-center gap-2">
                                <Input
                                    value={header.header_key}
                                    onChange={(event) => updateHeader(index, { header_key: event.target.value })}
                                    placeholder={t('advanced.headerKey')}
                                    className="flex-1 rounded-xl"
                                />
                                <Input
                                    value={header.header_value}
                                    onChange={(event) => updateHeader(index, { header_value: event.target.value })}
                                    placeholder={t('advanced.headerValue')}
                                    className="flex-1 rounded-xl"
                                />
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => removeHeader(index)}
                                    className="h-8 w-8 rounded-xl p-0 text-muted-foreground hover:bg-transparent hover:text-destructive"
                                    aria-label={t('advanced.removeHeader')}
                                >
                                    <X className="size-4" />
                                </Button>
                            </div>
                        ))}
                    </div>
                </div>

                <div className="space-y-2">
                    <label htmlFor="group-param-override" className="text-sm font-medium text-card-foreground">
                        {t('advanced.paramOverride')}
                    </label>
                    <textarea
                        id="group-param-override"
                        value={draft.param_override}
                        onChange={(event) => setDraft((current) => ({
                            ...current,
                            param_override: event.target.value,
                        }))}
                        placeholder={t('advanced.paramOverridePlaceholder')}
                        className="min-h-36 w-full rounded-xl border border-border bg-background px-3 py-2 font-mono text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    />
                </div>

                <div className="flex flex-col-reverse gap-2 pt-2 sm:flex-row sm:justify-end">
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={closeDialog}
                        className="rounded-xl"
                    >
                        {t('detail.actions.cancel')}
                    </Button>
                    <Button
                        type="button"
                        disabled={isSaving}
                        onClick={() => onSave(normalizeGroupRequestOptions(draft), closeDialog)}
                        className="rounded-xl"
                    >
                        {isSaving ? t('advanced.saving') : t('detail.actions.save')}
                    </Button>
                </div>
            </MorphingDialogDescription>
        </>
    );
}
