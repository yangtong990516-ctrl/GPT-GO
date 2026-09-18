// useRunLogStream —— 注册运行日志的 SSE 实时订阅 hook。
//
// 对接后端 GET /api/run-logs/runs/:runId/stream：
//   event: history  → data 为历史快照数组（一次性回放）
//   event: log      → data 为单条 RunLogEntry（实时增量）
//   event: done     → run 终态，服务端随后关闭连接
//   : ping          → 保活注释（EventSource 自动忽略）
//
// 设计要点（用户体验 + 健壮性）：
//   - 用 React ref 累积 entries，setState 触发渲染；避免闭包旧值问题。
//   - EventSource 自动重连（浏览器原生），重连后服务端会重发 history（覆盖重置）。
//   - paused 时丢弃增量（但保持连接与历史），恢复时不清空——适合「定格查看」。
//   - 组件卸载 / runId 变化自动 close（无泄漏）。
//   - done 后不再重连（标记 status=done，close EventSource）。
import { useEffect, useRef, useState, useCallback } from "react"
import type { RunLogEntry } from "./types"

export type StreamStatus = "idle" | "connecting" | "open" | "done" | "error"

export interface RunLogStreamState {
  entries: RunLogEntry[]
  status: StreamStatus
  /** 已通过 SSE 收到的增量条数（不含 history）。 */
  liveCount: number
}

const MAX_ENTRIES = 5000 // 前端内存上限（防长 run 刷爆，超出裁最旧）

export function useRunLogStream(runId: string | null, paused: boolean) {
  const [state, setState] = useState<RunLogStreamState>({
    entries: [],
    status: "idle",
    liveCount: 0,
  })
  const esRef = useRef<EventSource | null>(null)
  const pausedRef = useRef(paused)
  pausedRef.current = paused

  const reset = useCallback(() => {
    setState({ entries: [], status: "idle", liveCount: 0 })
  }, [])

  useEffect(() => {
    if (!runId) {
      setState({ entries: [], status: "idle", liveCount: 0 })
      return
    }
    // 关闭旧连接，开新连接。
    esRef.current?.close()
    setState({ entries: [], status: "connecting", liveCount: 0 })

    const url = `/api/run-logs/runs/${encodeURIComponent(runId)}/stream`
    const es = new EventSource(url)
    esRef.current = es

    es.addEventListener("history", (ev) => {
      try {
        const arr = JSON.parse((ev as MessageEvent).data) as RunLogEntry[]
        setState((s) => ({
          ...s,
          entries: Array.isArray(arr) ? arr.slice(-MAX_ENTRIES) : [],
          status: "open",
        }))
      } catch {
        /* 忽略坏帧 */
      }
    })

    es.addEventListener("log", (ev) => {
      if (pausedRef.current) return // 暂停：丢弃增量（保持历史定格）
      try {
        const entry = JSON.parse((ev as MessageEvent).data) as RunLogEntry
        setState((s) => {
          const next = s.entries.length >= MAX_ENTRIES
            ? [...s.entries.slice(1), entry]
            : [...s.entries, entry]
          return { ...s, entries: next, liveCount: s.liveCount + 1, status: "open" }
        })
      } catch {
        /* 忽略坏帧 */
      }
    })

    es.addEventListener("done", () => {
      setState((s) => ({ ...s, status: "done" }))
      es.close()
      esRef.current = null
    })

    es.onerror = () => {
      // EventSource 会自动重连；仅在已 done 时不标记 error。
      setState((s) => (s.status === "done" ? s : { ...s, status: "error" }))
    }

    return () => {
      es.close()
      esRef.current = null
    }
  }, [runId])

  return { ...state, reset }
}
