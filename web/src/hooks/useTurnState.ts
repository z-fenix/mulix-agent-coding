// useTurnState.ts——回合在飞态推导（task-12）：事件流归约——turn_started →
// running=true；turn_done / engine_exited → false。重放（对账帧）与实时帧同门
// 归约，running 起点即镜像尾部语义。纯推导（useMemo，无副作用）——驱动
// Composer 禁用/中断与 StatusBar 无关的本地交互态（FR-006/FR-007）。
import { useMemo } from 'react'
import type { WireFrame } from '../types'

export function useTurnState(frames: WireFrame[]): boolean {
  return useMemo(() => {
    let running = false
    for (const f of frames) {
      if (f.kind === 'turn_started') running = true
      else if (f.kind === 'turn_done' || f.kind === 'engine_exited') running = false
    }
    return running
  }, [frames])
}
