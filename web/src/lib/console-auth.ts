// 全局控制台登录门禁的 react-query hooks(整个 GPT-GO 一道密码)。
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "./api"

export interface ConsoleAuthStatus {
  setup_required: boolean
  authenticated: boolean
}

export function useConsoleAuthStatus() {
  return useQuery({
    queryKey: ["console", "auth", "status"],
    queryFn: () => api.get<ConsoleAuthStatus>("/api/auth/status"),
    retry: false,
    staleTime: 30_000,
  })
}

export function useConsoleLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { username: string; password: string }) =>
      api.post<{ admin: { username: string } }>("/api/auth/login", input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["console", "auth"] })
      // 登录成功后,刷新所有业务数据(此前因 401 失败的查询)。
      qc.invalidateQueries()
    },
  })
}

export function useConsoleLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<{ ok: boolean }>("/api/auth/logout"),
    onSuccess: () => {
      qc.clear()
      qc.invalidateQueries({ queryKey: ["console", "auth"] })
    },
  })
}

export function useConsoleChangePassword() {
  return useMutation({
    mutationFn: (input: { old_password: string; new_password: string }) =>
      api.post<{ ok: boolean }>("/api/auth/password", input),
  })
}
