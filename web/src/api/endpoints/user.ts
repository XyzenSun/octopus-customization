import { useEffect } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiClient, setAuthStoreGetter } from '../client';
import { logger } from '@/lib/logger';

/**
 * 用户登录请求
 */
export interface UserLoginRequest {
    username: string;
    password: string;
    expire: number; // token 过期时间（秒）
    code?: string; // TOTP 验证码，未开启两步验证时可省略
}

/**
 * 用户登录响应
 */
export interface UserLoginResponse {
    token: string;
    expire_at: string; // ISO 8601 格式
}

/**
 * 修改密码请求
 */
export interface ChangePasswordRequest {
    old_password: string;
    new_password: string;
}

/**
 * 修改用户名请求
 */
export interface ChangeUsernameRequest {
    new_username: string;
}

/**
 * 服务器时间与两步验证状态（登录页在未登录状态下调用）
 */
export interface ServerTimeResponse {
    server_time: string;
    timezone: string;
    two_factor_enabled: boolean;
}

/**
 * 两步验证绑定信息
 */
export interface TwoFactorSetupResponse {
    secret: string;
    uri: string;
    qr_code: string; // data:image/png;base64,... 可直接作为 <img src>
}

/**
 * 认证状态 Store
 */
interface AuthState {
    isAuthenticated: boolean;
    isLoading: boolean;
    isAPIKeyAuth: boolean;
    token: string | null;
    expireAt: string | null;

    // Actions
    setAuth: (token: string, expireAt: string) => void;
    setAPIKeyAuth: (apiKey: string) => void;
    checkAuth: () => Promise<void>;
    logout: () => void;
}

/**
 * 认证状态管理 Store（使用 zustand + persist）
 */
export const useAuthStore = create<AuthState>()(
    persist(
        (set, get) => ({
            isAuthenticated: false,
            isLoading: true,
            isAPIKeyAuth: false,
            token: null,
            expireAt: null,

            setAuth: (token: string, expireAt: string) => {
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: false,
                    token,
                    expireAt,
                    isLoading: false
                });
            },

            setAPIKeyAuth: (apiKey: string) => {
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: true,
                    token: apiKey,
                    expireAt: null,
                    isLoading: false
                });
            },

            checkAuth: async () => {
                const { token, expireAt, isAPIKeyAuth } = get();

                if (!token) {
                    set({ isAuthenticated: false, isLoading: false });
                    return;
                }

                // API Key 不检查本地过期时间
                if (!isAPIKeyAuth) {
                    if (!expireAt || Date.now() >= new Date(expireAt).getTime()) {
                        get().logout();
                        return;
                    }
                }

                try {
                    // API Key 模式只需校验 key 是否有效即可
                    const endpoint = isAPIKeyAuth ? '/api/v1/apikey/login' : '/api/v1/user/status';
                    await apiClient.get<unknown>(endpoint);
                    set({ isAuthenticated: true, isLoading: false });
                } catch (error) {
                    logger.error('认证验证失败:', error);
                    get().logout();
                }
            },

            logout: () => {
                set({
                    isAuthenticated: false,
                    isAPIKeyAuth: false,
                    token: null,
                    expireAt: null,
                    isLoading: false
                });
            }
        }),
        {
            name: 'auth-storage',
            partialize: (state) => ({
                token: state.token,
                expireAt: state.expireAt,
                isAPIKeyAuth: state.isAPIKeyAuth,
            })
        }
    )
);

// 注册 auth store getter 到 apiClient
if (typeof window !== 'undefined') {
    setAuthStoreGetter(() => {
        const state = useAuthStore.getState();
        return {
            token: state.token,
            logout: state.logout
        };
    });
}

/**
 * 用户登录 Hook
 * 
 * @example
 * const login = useLogin();
 * login.mutate({ username: 'admin', password: '123456', expire: 86400 });
 * 
 * if (login.isPending) return <Loading />;
 * if (login.isError) return <Error message={login.error.message} />;
 */
export function useLogin() {
    const { setAuth } = useAuthStore();

    return useMutation({
        mutationFn: async (data: UserLoginRequest) => {
            return apiClient.post<UserLoginResponse>('/api/v1/user/login', data);
        },
        onSuccess: (data) => {
            // 保存到 zustand store
            setAuth(data.token, data.expire_at);
        },
        onError: (error) => {
            logger.error('登录失败:', error);
        },
    });
}

/**
 * 修改密码 Hook
 * 
 * @example
 * const changePassword = useChangePassword();
 * changePassword.mutate({ oldPassword: '123', newPassword: '456' });
 */
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

/**
 * 修改用户名 Hook
 * 
 * @example
 * const changeUsername = useChangeUsername();
 * changeUsername.mutate({ newUsername: 'newname' });
 */
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

/**
 * 服务器时间与两步验证状态 Hook
 *
 * 登录页用它诊断 TOTP 时间偏差：TOTP 基于 Unix 时间戳，服务器与手机的绝对时间
 * 相差超过 30 秒就会算出不同的验证码。同时返回 two_factor_enabled，
 * 让登录表单决定是否展示验证码输入框。
 *
 * refetchInterval 让时间持续走动，方便用户实时比对手机时钟。
 */
export function useServerTime(enabled = true) {
    return useQuery({
        queryKey: ['server-time'],
        queryFn: async () => apiClient.get<ServerTimeResponse>('/api/v1/user/time'),
        enabled,
        refetchInterval: 1000,
        staleTime: 0,
        retry: false,
    });
}

/**
 * 生成两步验证密钥 Hook（绑定流程第一步）
 *
 * 每次调用都会生成新密钥覆盖旧的，但不会开启开关——必须再调 useEnableTwoFactor
 * 验证一次验证码才真正生效。
 */
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

/**
 * 启用两步验证 Hook（绑定流程第二步）
 */
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

/**
 * 关闭两步验证 Hook，需提供当前验证码
 */
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

/**
 * 认证状态和方法 Hook
 *
 * @example
 * const auth = useAuth();
 *
 * if (auth.isAuthenticated) {
 *   // 已登录
 * }
 *
 * auth.logout(); // 登出
 */
export function useAuth() {
    const store = useAuthStore();
    const { checkAuth, isLoading } = store;

    // 只在首次挂载时检查认证状态
    useEffect(() => {
        if (isLoading) {
            checkAuth();
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []); // 有意只在挂载时执行一次

    return {
        isAuthenticated: store.isAuthenticated,
        isAPIKeyAuth: store.isAPIKeyAuth,
        isLoading: store.isLoading,
        logout: store.logout,
    };
}

