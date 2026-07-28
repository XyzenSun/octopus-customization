import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';

/**
 * 熔断器状态条目（与后端 balancer.CircuitBreakerStatus 对齐）
 */
export interface CircuitBreakerStatus {
    channel_name: string;
    state: 'open' | 'half_open';
    remaining_cooldown: number; // 秒
}

/**
 * 查询当前处于熔断状态的通道列表
 * 策略：页面加载时立即获取，之后每 15 秒轮询一次
 */
export function useCircuitStatus() {
    return useQuery({
        queryKey: ['circuit', 'status'],
        queryFn: async () => {
            return apiClient.get<CircuitBreakerStatus[]>('/api/v1/circuit/status');
        },
        refetchInterval: 15000,
        refetchOnMount: 'always',
        refetchOnWindowFocus: false,
    });
}

/**
 * 重置所有熔断状态，成功后 invalidate 查询
 */
export function useResetCircuit() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: async () => {
            return apiClient.post<null>('/api/v1/circuit/reset');
        },
        onSuccess: () => {
            logger.log('熔断状态已全部重置');
            queryClient.invalidateQueries({ queryKey: ['circuit', 'status'] });
        },
        onError: (error) => {
            logger.error('重置熔断状态失败:', error);
        },
    });
}
