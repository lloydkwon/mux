export const meta = {
  name: 'mux-review',
  description: 'mux 커밋 전 검토 — mux-reviewer 와 다른 계열 모델이 같은 범위를 동시에 보고, 빈틈마다 두 관점의 반박자가 확인한다',
  whenToUse: 'mux 저장소에서 커밋 전에 변경을 검토할 때. args: { purpose: 목적과 수용 기준, scope: 검토할 경로·커밋 범위(이것 밖은 안 본다), docs?: 꼭 읽을 문서 경로 배열(건드린 파일의 docs/design 문서 — 디렉터리 CLAUDE.md 표에서 고른다), single?: true 면 다른 계열 검토를 건너뛴다 }',
  phases: [
    { title: 'Review', detail: 'mux-reviewer(Sonnet) 와 ocx-gpt-6-1-sol 이 같은 목적·범위를 동시에 검토' },
    { title: 'Verify', detail: '빈틈 지적마다 코드 관점·규칙 관점의 반박자 둘 — 둘 다 진짜라고 할 때만 남긴다' },
  ],
}

// 왜 이 모양인가(2026-11-23, CLAUDE.md «커밋 전 검토 · push 전 검증»):
// - 같은 모델의 재검토는 독립 검증이 아니다. 첫 시험에서 Sonnet 과 gpt-6.1 이 서로 다른 진짜 빈틈을 하나씩 잡았다.
// - 검토자의 지적은 그럴듯하지만 틀릴 수 있다. 반영하기 전에 다른 에이전트가 반박해 본다 — 코드에서 실제로 일어나는지,
//   목적·규칙상 진짜 빈틈인지. 둘 다 «진짜» 일 때만 확인된 빈틈이다.
// - 선택 사항은 반박하지 않고 그대로 돌려준다. 반영하지 않는 것이 규칙이라 확인에 에이전트를 쓰지 않는다.
// 이 Workflow 는 고치지 않는다. 반영·재검토(두 번까지)는 부른 세션이 한다.

if (!args || typeof args.purpose !== 'string' || typeof args.scope !== 'string' || !args.purpose.trim() || !args.scope.trim()) {
  throw new Error('args 에 purpose(목적·수용 기준)와 scope(검토할 경로·커밋 범위) 문자열이 필요하다')
}
const PURPOSE = args.purpose.trim()
const SCOPE = args.scope.trim()
// 꼭 읽을 문서. 2026-11-23 첫 실행에서 보관함 검토자가 park.md 를 읽지 않고 18초 만에 «clean» 을 냈다 —
// 읽었는지는 결과의 read 와 대조해 빠지면 알린다(조용히 넘어가지 않는다).
const DOCS = Array.isArray(args.docs) ? args.docs.filter(d => typeof d === 'string' && d.trim()).map(d => d.trim()) : []

const FINDINGS = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          kind: { type: 'string', enum: ['gap', 'optional'], description: 'gap = 정확성·요구사항 빈틈, optional = 선택 사항' },
          file: { type: 'string', description: '저장소 기준 경로' },
          line: { type: 'integer', description: '그 파일의 줄 (모르면 0)' },
          claim: { type: 'string', description: '무엇이 문제인가' },
          why: { type: 'string', description: '왜 문제인가 — 틀리는 입력·상태' },
          basis: { type: 'string', description: '근거가 된 규칙 줄이나 docs/design 문서의 절 (없으면 빈 문자열)' },
          fix: { type: 'string', description: '어떻게 고치는가 (선택 사항이면 빈 문자열 가능)' },
        },
        required: ['kind', 'file', 'line', 'claim', 'why', 'basis', 'fix'],
      },
    },
    read: { type: 'array', items: { type: 'string' }, description: '읽은 CLAUDE.md·docs/design 문서 경로' },
  },
  required: ['findings', 'read'],
}

const VERDICT = {
  type: 'object',
  properties: {
    real: { type: 'boolean', description: '반박하지 못했으면 true. 확신이 없으면 false' },
    reason: { type: 'string', description: '판단의 근거 — 파일:줄, 읽은 규칙, 돌려 본 명령과 그 출력' },
    scenario: { type: 'string', description: 'real 이면 틀리는 구체적인 입력·상태, 아니면 빈 문자열' },
  },
  required: ['real', 'reason', 'scenario'],
}

const TASK = `## 이번 변경의 목적
${PURPOSE}

## 검토할 범위 — 이것만 본다
${SCOPE}

작업 트리에는 범위 밖의 다른 미커밋 작업이 섞여 있을 수 있다. 범위 밖은 보지 않는다.${DOCS.length ? `

## 꼭 읽을 문서 — 판단 전에 끝까지 읽는다
${DOCS.map(d => `- ${d}`).join('\n')}` : ''}`

const OUTPUT_RULE = `결과는 StructuredOutput 의 칸에 담는다. 정확성·요구사항 빈틈은 kind "gap", 선택 사항은 kind "optional".
basis 에는 근거가 된 규칙 줄(루트·디렉터리 CLAUDE.md)이나 docs/design 문서의 절을 적는다. read 에는 실제로 읽은 기준 문서를 적는다.
빈틈이 없으면 findings 를 빈 배열로 둔다. 파일을 고치지 않고, 커밋·push 하지 않고, 빌드·테스트를 돌리지 않는다.`

const REVIEWERS = [
  {
    name: 'mux-reviewer',
    agentType: 'mux-reviewer',
    prompt: `${TASK}\n\n검토자 정의의 절차와 기준을 그대로 따른다.\n${OUTPUT_RULE}`,
  },
  {
    name: 'gpt-6.1',
    agentType: 'ocx-gpt-6-1-sol',
    // 진짜 모델은 opencodex 프록시가 정한다. model 을 비우면 하네스가 정의의 모델 이름(ocx-claude-native--…)을
    // 직접 찾다가 «선택된 모델에 문제가 있다» 로 실패한다(2026-11-23). 정의가 시키는 대로 haiku 를 자리표시로 넘긴다.
    model: 'haiku',
    prompt: `먼저 저장소 루트의 \`.claude/agents/mux-reviewer.md\` 를 읽고, 그 파일이 정한 검토자로서 그 절차와 기준을 그대로 따른다. 답은 한국어로 쓴다.\n\n${TASK}\n\n${OUTPUT_RULE}`,
  },
]
const active = args.single === true ? REVIEWERS.slice(0, 1) : REVIEWERS
if (active.length < REVIEWERS.length) log('single: 다른 계열 검토(gpt-6.1)를 건너뛴다')

// ── Review — 두 검토자를 동시에. 같은 자리를 둘 다 짚었는지 보려면 둘의 결과가 다 있어야 한다(장벽이 맞다).
phase('Review')
const reviews = await parallel(active.map(r => () =>
  agent(r.prompt, { label: `검토:${r.name}`, phase: 'Review', agentType: r.agentType, schema: FINDINGS, ...(r.model ? { model: r.model } : {}) })))

const results = reviews.map((res, i) => ({ who: active[i].name, res }))
const missing = results.filter(r => !r.res).map(r => r.who)
missing.forEach(who => log(`${who} 검토가 결과 없이 끝났다 — 그 검토자는 빠진 채 진행한다`))
if (missing.length === active.length) throw new Error('검토자 모두 결과 없이 끝났다 — 다시 돌린다')
const done = results.filter(r => r.res)
const read = Object.fromEntries(done.map(r => [r.who, r.res.read]))
const all = done.flatMap(r => r.res.findings.map(f => ({ ...f, by: r.who })))
// 꼭 읽을 문서를 안 읽은 검토자 — 경로 끝이 같으면 읽은 것으로 본다(절대·상대 경로 차이). 검토자가 경로 뒤에
// «(전체)» 같은 주석을 붙여 적기도 해서 첫 공백·괄호 앞까지만 경로로 본다(2026-11-23, 다 읽은 gpt-6.1 이 «안 읽음» 으로 잡혔다).
const pathOf = p => p.trim().split(/[\s(]/)[0]
const shallow = Object.fromEntries(done
  .map(r => [r.who, DOCS.filter(d => !r.res.read.map(pathOf).some(p => p.endsWith(d) || d.endsWith(p)))])
  .filter(([, miss]) => miss.length > 0))
Object.entries(shallow).forEach(([who, miss]) => log(`${who} 가 꼭 읽을 문서를 안 읽었다: ${miss.join(', ')} — 그 검토의 «빈틈 없음» 을 믿지 않는다`))

// 빈틈 지적은 하나씩 반박한다. 같은 자리(같은 파일 ±3줄)의 다른 지적은 «겹침» 표시와 반박자에게 줄 맥락으로만 쓴다.
// 처음에는 자리별로 묶어 묶음째 반박했는데, 틀린 설계 주장에 딸린 맞는 지적(테스트가 그 경로를 안 잰다)이 함께
// «반박됨» 으로 떨어졌다(2026-11-23 보관함 검토).
const near = (a, b) => a.file === b.file && Math.abs(a.line - b.line) <= 3
const text = i => `[${i.by}·${i.kind}] ${i.claim} — ${i.why}${i.basis ? ` (근거: ${i.basis})` : ''}${i.fix ? ` · 고치기: ${i.fix}` : ''}`
const shape = f => {
  const around = all.filter(o => o !== f && near(o, f))
  return {
    file: f.file,
    line: f.line,
    by: f.by,
    overlap: around.some(o => o.by !== f.by),
    claim: text(f),
    context: around.map(text),
  }
}
const gaps = all.filter(f => f.kind === 'gap').map(shape)
const optional = all.filter(f => f.kind === 'optional').map(shape)
log(`지적 ${all.length}개: 빈틈 후보 ${gaps.length}개(다른 검토자와 겹침 ${gaps.filter(g => g.overlap).length}), 선택 사항 ${optional.length}개`)

// ── Verify — 빈틈 후보마다 두 관점이 반박해 본다. 후보끼리는 서로 기다리지 않는다.
const LENSES = [
  {
    key: 'code',
    text: `코드 관점: 이 지적이 실제 코드에서 일어나는지 반박해 본다. 일어나는 구체적인 입력·상태를 코드에서 만들 수 있으면 real=true 와 scenario 를 적는다. 만들지 못하면 real=false. 확신이 없으면 real=false.
읽기 명령과 \`git diff\` 를 쓰고, 필요하면 \`go test ./<패키지> -run <이름>\` 이나 스크래치 디렉터리의 작은 실험으로 확인해도 된다. \`make verify\`·e2e 는 돌리지 않는다(동시에 도는 e2e 는 하나여야 한다).`,
  },
  {
    key: 'rule',
    text: `규칙 관점: 이 지적이 이번 목적·수용 기준과 저장소 규칙(루트·디렉터리 CLAUDE.md, 그것이 가리키는 docs/design 문서) 기준으로 진짜 빈틈인지 반박해 본다. 목적 밖의 요구, 취향·스타일, 근거로 든 규칙이 실제로 없거나 다른 뜻이면 real=false. 확신이 없으면 real=false.`,
  },
]
const verdicts = await parallel(gaps.map(g => () =>
  parallel(LENSES.map(l => () =>
    agent(`${TASK}

## 반박해 볼 지적 — ${g.file}:${g.line}
${g.claim}
${g.context.length ? `
같은 자리의 다른 지적(맥락일 뿐 — 판정은 위 지적 하나만 한다):
${g.context.join('\n')}
` : ''}
${l.text}
파일을 고치지 않는다. 커밋·push 하지 않는다.`, { label: `확인:${l.key}:${g.file}:${g.line}`, phase: 'Verify', schema: VERDICT })))
    .then(vs => ({ ...g, code: vs[0], rule: vs[1] }))))

// 두 반박자 중 하나라도 결과 없이 끝났으면 판정하지 못한 것이다(반박된 것으로 세지 않는다).
const decided = verdicts.filter(v => v && v.code && v.rule)
const confirmed = decided.filter(v => v.code.real && v.rule.real)
const refuted = decided.filter(v => !(v.code.real && v.rule.real))
const unverified = gaps.length - decided.length
if (unverified > 0) log(`빈틈 후보 ${unverified}개는 확인 에이전트가 결과 없이 끝나 판정하지 못했다`)
log(`확인된 빈틈 ${confirmed.length}개 · 반박된 것 ${refuted.length}개 · 선택 사항 ${optional.length}개`)

// 검토자가 빠졌거나 꼭 읽을 문서를 안 읽었으면, 확인된 빈틈이 없어도 «clean» 이라 하지 않는다.
const incomplete = missing.length > 0 || Object.keys(shallow).length > 0 || unverified > 0
return {
  verdict: confirmed.length > 0 ? 'gaps' : incomplete ? 'incomplete' : 'clean',
  confirmed,
  refuted,
  optional,
  unverified,
  missing_reviewers: missing,
  shallow,
  read,
}
