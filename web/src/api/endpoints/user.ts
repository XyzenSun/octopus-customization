import { useEffect } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiClient, setAuthStoreGetter, type ClientAuthSession } from '../client';
import { logger } from '@/lib/logger';

export interface UserLoginRequest {
    username: string;
    password: string;
    code?: string;
}

interface AdminSessionResponse {
    kind: 'admin';
}

export interface ChangePasswordRequest {
    old_password: string;
    new_password: string;
}

export interface ChangeUsernameRequest {
    new_username: string;
}

export interface ServerTimeResponse {
    server_time: string;
    timezone: string;
    two_factor_enabled: boolean;
}

export interface TwoFactorSetupResponse {
    secret: string;
    uri: string;
    qr_code: string;
}

export type AuthSession = ClientAuthSession;

type PersistedAuthState = {
    session: AuthSession;
};

interface AuthState {
    session: AuthSession;
    isAuthenticated: boolean;
    isLoading: boolean;
    isAPIKeyAuth: boolean;

    setAdminAuth: () => void;
    setAPIKeyAuth: (apiKey: string) => void;
    checkAuth: () => Promise<void>;
    logout: () => void;
}

function authFlags(session: AuthSession) {
    return {
        isAuthenticated: session.kind === 'admin' || session.kind === 'apiKey',
        isAPIKeyAuth: session.kind === 'apiKey',
        isLoading: session.kind === 'checking',
    };
}

function persistedSession(session: AuthSession): AuthSession {
    return session.kind === 'apiKey'
        ? session
        : { kind: 'anonymous' };
}

function migrateAuthState(persistedState: unknown): PersistedAuthState {
    if (!persistedState || typeof persistedState !== 'object') {
        return { session: { kind: 'anonymous' } };
    }

    const state = persistedState as {
        session?: AuthSession;
        token?: string | null;
        isAPIKeyAuth?: boolean;
    };
    if (state.session?.kind === 'apiKey' && state.session.apiKey) {
        return { session: state.session };
    }
    if (state.isAPIKeyAuth && state.token) {
        return { session: { kind: 'apiKey', apiKey: state.token } };
    }

    // 旧管理员 JWT 不转换为 Cookie；升级后重新登录，避免保留双认证协议。
    return { session: { kind: 'anonymous' } };
}

export const useAuthStore = create<AuthState>()(
    persist<AuthState, [], [], PersistedAuthState>(
        (set, get) => ({
            session: { kind: 'checking' },
            isAuthenticated: false,
            isLoading: true,
            isAPIKeyAuth: false,

            setAdminAuth: () => {
                const session: AuthSession = { kind: 'admin' };
                set({ session, ...authFlags(session) });
            },

            setAPIKeyAuth: (apiKey: string) => {
                const session: AuthSession = { kind: 'apiKey', apiKey };
                set({ session, ...authFlags(session) });
            },

            checkAuth: async () => {
                const persisted = get().session;
                const checkingSession: AuthSession = persisted.kind === 'apiKey'
                    ? { kind: 'checking', apiKey: persisted.apiKey }
                    : { kind: 'checking' };
                set({ session: checkingSession, ...authFlags(checkingSession) });

                try {
                    if (checkingSession.apiKey) {
                        await apiClient.get<unknown>('/api/v1/apikey/login');
                        const session: AuthSession = { kind: 'apiKey', apiKey: checkingSession.apiKey };
                        set({ session, ...authFlags(session) });
                        return;
                    }

                    await apiClient.get<AdminSessionResponse>('/api/v1/user/status');
                    const session: AuthSession = { kind: 'admin' };
                    set({ session, ...authFlags(session) });
                } catch (error) {
                    logger.error('认证验证失败:', error);
                    const session: AuthSession = { kind: 'anonymous' };
                    set({ session, ...authFlags(session) });
                }
            },

            logout: () => {
                const previousSession = get().session;
                const session: AuthSession = { kind: 'anonymous' };
                set({ session, ...authFlags(session) });

                if (previousSession.kind === 'admin') {
                    void apiClient.post<null>('/api/v1/user/logout', {}).catch((error) => {
                        logger.error('退出登录失败:', error);
                    });
                }
            },
        }),
        {
            name: 'auth-storage',
            version: 2,
            partialize: (state) => ({ session: persistedSession(state.session) }),
            migrate: (state) => migrateAuthState(state),
            merge: (persisted, current) => {
                const session = (persisted as PersistedAuthState | undefined)?.session ?? { kind: 'anonymous' };
                return {
                    ...current,
                    session,
                    ...authFlags({ kind: 'checking' }),
                };
            },
        }
    )
);

if (typeof window !== 'undefined') {
    setAuthStoreGetter(() => {
        const state = useAuthStore.getState();
        return {
            session: state.session,
            logout: state.logout,
        };
    });
}

export function useLogin() {
    const { setAdminAuth } = useAuthStore();

    return useMutation({
        mutationFn: (data: UserLoginRequest) =>
            apiClient.post<AdminSessionResponse>('/api/v1/user/login', data),
        onSuccess: () => {
            setAdminAuth();
        },
        onError: (error) => {
            logger.error('登录失败:', error);
        },
    });
}

export function useChangePassword() {
    return useMutation({
        mutationFn: async (data: { oldPassword: string; newPassword: string }) => {
            const payload: ChangePasswordRequest = {
                old_password: data.oldPassword,
                new_password: data.newPassword,
            };
            return apiClient.post<string>('/api/v1/user/change-password', payload);
        },
        onSuccess: (message) => {
            logger.log('密码修改成功:', message);
        },
        onError: (error) => {
            logger.error('密码修改失败:', error);
        },
    });
}

export function useChangeUsername() {
    return useMutation({
        mutationFn: async (data: { newUsername: string }) => {
            const payload: ChangeUsernameRequest = {
                new_username: data.newUsername,
            };
            return apiClient.post<string>('/api/v1/user/change-username', payload);
        },
        onSuccess: (message) => {
            logger.log('用户名修改成功:', message);
        },
        onError: (error) => {
            logger.error('用户名修改失败:', error);
        },
    });
}

export async function fetchServerTime() {
    return apiClient.get<ServerTimeResponse>('/api/v1/user/time');
}

export function useServerTime(enabled = true) {
    return useQuery({
        queryKey: ['server-time'],
        queryFn: fetchServerTime,
        enabled,
        staleTime: Infinity,
        refetchOnWindowFocus: false,
        retry: false,
    });
}

export function useSetupTwoFactor() {
    return useMutation({
        mutationFn: async () => {
            return apiClient.post<TwoFactorSetupResponse>('/api/v1/user/2fa/setup', {});
        },
        onError: (error) => {
            logger.error('两步验证初始化失败:', error);
        },
    });
}

export function useEnableTwoFactor() {
    return useMutation({
        mutationFn: async (code: string) => {
            return apiClient.post<string>('/api/v1/user/2fa/enable', { code });
        },
        onError: (error) => {
            logger.error('两步验证启用失败:', error);
        },
    });
}

export function useDisableTwoFactor() {
    return useMutation({
        mutationFn: async (code: string) => {
            return apiClient.post<string>('/api/v1/user/2fa/disable', { code });
        },
        onError: (error) => {
            logger.error('两步验证关闭失败:', error);
        },
    });
}

export function useAuth() {
    const store = useAuthStore();
    const { checkAuth, isLoading } = store;

    useEffect(() => {
        if (isLoading) {
            void checkAuth();
        }
        // 有意只在挂载时检查一次，后续 401 由 apiClient 统一退出。
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return {
        session: store.session,
        isAuthenticated: store.isAuthenticated,
        isAPIKeyAuth: store.isAPIKeyAuth,
        isLoading: store.isLoading,
        logout: store.logout,
    };
}
