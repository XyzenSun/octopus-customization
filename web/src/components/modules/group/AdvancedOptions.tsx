'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import type { CustomHeader } from '@/api/endpoints/channel';
import { Button } from '@/components/ui/button';
import {
    CustomHeaderEditor,
    createEditableCustomHeaders,
    normalizeCustomHeaders,
    type EditableCustomHeader,
} from '@/components/common/CustomHeaderEditor';
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
import { cn } from '@/lib/utils';

export type GroupRequestOptions = {
    custom_header: CustomHeader[];
    param_override: string;
};

type GroupRequestOptionsDraft = {
    custom_header: EditableCustomHeader[];
    param_override: string;
};

function normalizeGroupRequestOptions(options: GroupRequestOptionsDraft): GroupRequestOptions {
    return {
        custom_header: normalizeCustomHeaders(options.custom_header),
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
    const [draft, setDraft] = useState<GroupRequestOptionsDraft>(() => ({
        custom_header: createEditableCustomHeaders(value.custom_header),
        param_override: value.param_override,
    }));

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
                <CustomHeaderEditor
                    headers={draft.custom_header}
                    onChange={(customHeader) => setDraft((current) => ({ ...current, custom_header: customHeader }))}
                    label={t('advanced.customHeader')}
                    addLabel={t('advanced.addHeader')}
                    keyPlaceholder={t('advanced.headerKey')}
                />

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
