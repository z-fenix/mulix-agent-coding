import type { EngineInterface, RenderElement, RenderInput } from 'claude-code'
import type { MulixWorkbenchState } from '../types'
import { cacheHit } from './model'

export type MulixWorkbenchActions = {
  refresh(): Promise<void>
  select(view: 'workflow' | 'usage'): Promise<void>
  close(): Promise<void>
}

const controls = /[\x00-\x1f\x7f-\x9f\u{61c}\u{200e}-\u{200f}\u{2028}-\u{202e}\u{2066}-\u{2069}]/gu
const clean = (value: string) => value.replace(controls, '�').slice(0, 4096)
const stamp = (value: number | null | undefined) => value === null || value === undefined ? '未提供' : new Date(value).toISOString()

export function renderWorkbench(
  $: EngineInterface,
  e: RenderInput<'Pane'>,
  state: MulixWorkbenchState,
  actions: MulixWorkbenchActions,
): RenderElement {
  const { Box, Text, Button } = $.ui.resolve(e)
  const source = state.nativeUsage
  const observed = state.observation
  const workflow = state.workflow
  const fields = workflow?.fields
  const ratio = cacheHit(state)
  const value = (n: number) => observed.validTurns === 0 ? '尚未观察有效用量' : String(n)
  const context = source?.tokens === null || source?.tokens === undefined
    ? `上下文：未提供（窗口 ${source?.window ?? '未提供'}）`
    : `上下文：${source.tokens} / ${source.window ?? '未提供'}（${source.percent === null ? '百分比未提供' : `${source.percent}%`}）`

  return (
    <Box flexDirection="column">
      <Text>mulix 只读工作台 · 不执行任务或批准</Text>
      {state.ui.busy && <Text>正在读取工作流；再次刷新不会重复启动查询</Text>}
      {state.warning && <Text>{clean(state.warning)}</Text>}
      {state.ui.error && <Text>{clean(state.ui.error)}</Text>}
      {e.props.view.agentId && <Text>旁侧为代理视图；此处仍统计原生主会话</Text>}
      {state.ui.view === 'usage' ? (
        <Box flexDirection="column">
          <Text key="usage-provider">提供方：Claude Code 原生宿主</Text>
          <Text key="usage-session">原生主会话：{clean(state.identity?.sessionId ?? '未提供')}（不是产品会话账本）</Text>
          <Text key="usage-context">{context}</Text>
          <Text key="usage-window">观察窗口：{stamp(observed.since)} — {stamp(observed.until)}（仅启用后完整主回合，非全会话账本）</Text>
          <Text key="usage-valid-turns">有效用量回合：{observed.validTurns}；缺失 {observed.missingTurns}，非法 {observed.invalidTurns}</Text>
          <Text key="usage-input">未缓存输入：{value(observed.counts.input)}</Text>
          <Text key="usage-write">缓存写入：{value(observed.counts.write)}</Text>
          <Text key="usage-read">缓存读取：{value(observed.counts.read)}</Text>
          <Text key="usage-output">输出：{value(observed.counts.output)}</Text>
          <Text key="usage-hit">命中率：{ratio === null ? '不可计算（尚无可计算输入）' : `${(ratio * 100).toFixed(1)}%`}</Text>
          <Text key="usage-cost">费用不可得：{source?.cost.reason === 'pricing-coverage-unknown' ? '原生接口未披露定价覆盖' : '宿主未提供费用'}</Text>
          {observed.models.length > 0 && <Text>观察模型：{clean(observed.models.join('、'))}</Text>}
          {observed.frozen && <Text>观察已达 4096 回合上限，窗口冻结；不覆盖后续回合</Text>}
        </Box>
      ) : (
        <Box flexDirection="column">
          <Text key="workflow-source">数据源：mulix 工作流 CLI（只读）</Text>
          <Text key="workflow-root">仓库根目录：{clean(state.identity?.root ?? '未提供')}</Text>
          <Text key="workflow-change">变更：{clean(fields?.change ?? '未提供')}</Text>
          <Text key="workflow-phase">阶段：{fields?.phase ?? '未提供'}</Text>
          <Text key="workflow-design">设计批准：{fields === null || fields === undefined ? '未知' : String(fields.designApproved)}</Text>
          <Text key="workflow-verify">验证结果：{fields?.verify ?? '未提供'}</Text>
          <Text key="workflow-observed-at">观察时间：{stamp(workflow?.observedAt)}</Text>
          <Text key="workflow-stale">数据状态：{workflow?.stale ? '过期（读取失败，保留同根快照）' : fields ? '当前' : '不可得'}</Text>
          {workflow?.reason && <Text>{clean(workflow.reason)}</Text>}
        </Box>
      )}
      <Box flexDirection="row">
        <Button key="workflow" label="工作流" onPress={() => actions.select('workflow')} />
        <Button key="usage" label="原生用量" onPress={() => actions.select('usage')} />
        <Button key="refresh" label="刷新" onPress={() => actions.refresh()} />
        <Button key="close" label="关闭" onPress={() => actions.close()} />
      </Box>
    </Box>
  )
}
