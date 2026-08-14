import type { ApiError } from './types';
import { HttpStatus } from './types';

export const API_BASE_URL = '';

export type ClientAuthSession =
    | { kind: 'checking'; apiKey?: string }
    | { kind: 'anonymous' }
    | { kind: 'admin' }
    | { kind: 'apiKey'; apiKey: string };

let getAuthStore: (() => { session: ClientAuthSession; logout: () => void }) | null = null;

export function setAuthStoreGetter(getter: () => { session: ClientAuthSession; logout: () => void }) {
    getAuthStore = getter;
}

const handleError = (error: ApiError) => {
    console.error('API Error:', error);

    if (error.code === HttpStatus.UNAUTHORIZED && getAuthStore) {
        getAuthStore().logout();
    }
};

async function handleResponse<T>(response: Response): Promise<T> {
    const contentType = response.headers.get('content-type');
    const isJson = contentType?.includes('application/json');

    let data: unknown;
    if (isJson) {
        data = await response.json();
    } else {
        data = await response.text();
    }

    if (!response.ok) {
        const error: ApiError = {
            code: response.status,
            message: (data && typeof data === 'object' && 'message' in data && typeof data.message === 'string')
                ? data.message
                : (typeof data === 'string' ? data : response.statusText),
        };

        handleError(error);
        throw error;
    }

    if (data && typeof data === 'object' && 'data' in data) {
        return data.data as T;
    }

    return data as T;
}

async function request<T>(
    method: string,
    path: string,
    body?: BodyInit,
    params?: Record<string, string | number | boolean>
): Promise<T> {
    const searchParams = params ? new URLSearchParams(
        Object.entries(params).map(([key, value]) => [key, String(value)])
    ).toString() : '';
    const url = `${API_BASE_URL}${path}${searchParams ? `?${searchParams}` : ''}`;

    const headers = new Headers();
    if (body) {
        headers.set('Content-Type', 'application/json');
    }

    // 管理员凭据由浏览器通过 HttpOnly Cookie 携带；只有 API Key 身份发送 Bearer。
    if (typeof window !== 'undefined' && getAuthStore) {
        const { session } = getAuthStore();
        if ((session.kind === 'apiKey' || session.kind === 'checking') && session.apiKey) {
            headers.set('Authorization', `Bearer ${session.apiKey}`);
        }
    }

    const response = await fetch(url, {
        method,
        headers,
        body,
        credentials: 'same-origin',
    });

    return handleResponse<T>(response);
}

export const apiClient = {
    get: <T>(path: string, params?: Record<string, string | number | boolean>): Promise<T> =>
        request<T>('GET', path, undefined, params),

    post: <T>(path: string, data?: unknown, params?: Record<string, string | number | boolean>): Promise<T> =>
        request<T>('POST', path, data ? JSON.stringify(data) : undefined, params),

    put: <T>(path: string, data?: unknown, params?: Record<string, string | number | boolean>): Promise<T> =>
        request<T>('PUT', path, data ? JSON.stringify(data) : undefined, params),

    delete: <T>(path: string, params?: Record<string, string | number | boolean>): Promise<T> =>
        request<T>('DELETE', path, undefined, params),

    patch: <T>(path: string, data?: unknown, params?: Record<string, string | number | boolean>): Promise<T> =>
        request<T>('PATCH', path, data ? JSON.stringify(data) : undefined, params),
};
