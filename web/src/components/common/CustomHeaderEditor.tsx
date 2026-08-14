'use client';

import { Plus, X } from 'lucide-react';
import { useTranslations } from 'next-intl';
import type { CustomHeader } from '@/api/endpoints/channel';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';

export type CustomHeaderAction = 'set' | 'empty' | 'remove';

// action 只服务于前端编辑体验，不会提交到 API 或写入数据库。
// header_value 始终保留用户最后输入的草稿，切换到空值/删除后再切回来时无需重新输入。
export type EditableCustomHeader = {
    header_key: string;
    header_value: string;
    action: CustomHeaderAction;
};

export function createEditableCustomHeader(header?: CustomHeader): EditableCustomHeader {
    if (!header) {
        return { header_key: '', header_value: '', action: 'set' };
    }
    if (header.header_value === null) {
        return { header_key: header.header_key, header_value: '', action: 'remove' };
    }
    if (header.header_value === '') {
        return { header_key: header.header_key, header_value: '', action: 'empty' };
    }
    return { header_key: header.header_key, header_value: header.header_value, action: 'set' };
}

export function createEditableCustomHeaders(headers?: CustomHeader[] | null): EditableCustomHeader[] {
    return headers && headers.length > 0
        ? headers.map(createEditableCustomHeader)
        : [createEditableCustomHeader()];
}

export function normalizeCustomHeaders(headers: EditableCustomHeader[]): CustomHeader[] {
    return headers
        .map((header): CustomHeader => {
            const headerValue = header.action === 'remove'
                ? null
                : header.action === 'empty'
                    ? ''
                    : header.header_value;
            return {
                header_key: header.header_key.trim(),
                header_value: headerValue,
            };
        })
        .filter((header) => header.header_key !== '');
}

export function CustomHeaderEditor({
    headers,
    onChange,
    label,
    addLabel,
    keyPlaceholder,
    showCount = false,
}: {
    headers: EditableCustomHeader[];
    onChange: (headers: EditableCustomHeader[]) => void;
    label: string;
    addLabel: string;
    keyPlaceholder: string;
    showCount?: boolean;
}) {
    const t = useTranslations('common.customHeaderEditor');

    const updateHeader = (index: number, patch: Partial<EditableCustomHeader>) => {
        onChange(headers.map((header, currentIndex) => (
            currentIndex === index ? { ...header, ...patch } : header
        )));
    };

    const addHeader = () => {
        onChange([...headers, createEditableCustomHeader()]);
    };

    const removeHeader = (index: number) => {
        const nextHeaders = headers.filter((_, currentIndex) => currentIndex !== index);
        onChange(nextHeaders.length > 0 ? nextHeaders : [createEditableCustomHeader()]);
    };

    return (
        <div className="space-y-2">
            <div className="flex items-center justify-between">
                <label className="text-sm font-medium text-card-foreground">
                    {label} {showCount && headers.length > 0 ? `(${headers.length})` : ''}
                </label>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={addHeader}
                    className="h-6 px-2 text-xs text-muted-foreground/70 hover:bg-transparent hover:text-muted-foreground"
                >
                    <Plus className="mr-1 size-3" />
                    {addLabel}
                </Button>
            </div>

            <div className="space-y-2">
                {headers.map((header, index) => (
                    <div
                        key={`custom-header-${index}`}
                        className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_2rem] items-center gap-2"
                    >
                        <Input
                            type="text"
                            value={header.header_key}
                            onChange={(event) => updateHeader(index, { header_key: event.target.value })}
                            placeholder={keyPlaceholder}
                            className="rounded-xl"
                        />
                        <Select
                            value={header.action}
                            onValueChange={(action) => updateHeader(index, { action: action as CustomHeaderAction })}
                        >
                            <SelectTrigger className="w-full rounded-xl" aria-label={t('actionLabel')}>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="set">{t('set')}</SelectItem>
                                <SelectItem value="empty">{t('empty')}</SelectItem>
                                <SelectItem value="remove">{t('remove')}</SelectItem>
                            </SelectContent>
                        </Select>
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => removeHeader(index)}
                            className="h-8 w-8 rounded-xl p-0 text-muted-foreground hover:bg-transparent hover:text-destructive"
                            aria-label={t('removeRule')}
                            title={t('removeRule')}
                        >
                            <X className="size-4" />
                        </Button>

                        {header.action === 'set' ? (
                            <Input
                                type="text"
                                value={header.header_value}
                                onChange={(event) => updateHeader(index, { header_value: event.target.value })}
                                placeholder={t('valuePlaceholder')}
                                className="col-span-2 rounded-xl"
                            />
                        ) : (
                            <div className="col-span-2 flex h-9 items-center rounded-xl border border-border bg-muted/40 px-3 text-xs text-muted-foreground">
                                {header.action === 'empty' ? t('emptyDescription') : t('removeDescription')}
                            </div>
                        )}
                    </div>
                ))}
            </div>
        </div>
    );
}
