// mux serve 의 폰 화면(docs/design/serve.md). 서버는 갈래와 값만 내고, 문구와 모양은 여기서 정한다.
// 글은 언제나 textContent 로 넣는다 — pane 내용·세션 이름·회신은 남이 정할 수 있는 글이다. innerHTML 은
// 고정된 아이콘 문자열에만 쓴다.
"use strict";
(() => {
  const POLL_MS = 2000;
  const LONG_PRESS_MS = 500;
  const MAX_RUNES = 700; // tmux.maxPromptRunes 와 같다. 정본은 서버의 검사다.

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  };
  const clock = (ms) => new Date(ms).toTimeString().slice(0, 8);
  const hhmm = (ms) => clock(ms).slice(0, 5);

  const STATE_LABEL = { working: "작업 중", approval: "승인 대기", ready: "완료", shell: "셸 실행 중" };
  const AGENT_WORD = { working: "작업 중", idle: "입력 기다림", blocked: "승인 대기", shell: "셸 실행 중", unknown: "상태 모름" };

  let list = { ai: [], other: [] };
  let loaded = false;
  // 보낼 곳은 이름으로 기억한다 — 목록은 2초마다 새로 오고, 객체는 그때마다 바뀐다.
  let sel = { session: null, pane: null };
  const page = {
    open: false, session: null, tab: "talk", stick: true, turn: null, turnFor: "", turnKey: "", talkSig: "",
    // 답을 기다리는 대화상자와 고르던 선택지({n, label, free, key}), Esc 를 확인 중인 대화상자의 열쇠.
    // key 는 서버가 준 대화상자 열쇠다 — 보낼 때 그대로 돌려보내, 그사이 다른 대화상자가 떴으면 서버가 누르지 않는다.
    dialog: null, dialogFor: "", dialogSig: "", pending: null, confirmCancel: "",
    multi: null, // 여러 개 고르기의 폰 쪽 체크 {key, checked: {n: bool}}
  };

  // ── 서버 ────────────────────────────────────────────────────────────
  // status 0 은 응답을 받지 못했다는 뜻이다 — 요청이 서버에 닿았는지 모른다(보내기에서는 «모름» 으로 다룬다).
  class ApiError extends Error {
    constructor(message, status) {
      super(message);
      this.status = status;
    }
  }
  async function api(path, init) {
    let res;
    try {
      res = await fetch(path, { cache: "no-store", credentials: "same-origin", ...init });
    } catch (_) {
      throw new ApiError("서버의 답을 받지 못했습니다. 폰의 연결과 mux serve·tailscale serve 가 떠 있는지 확인하세요.", 0);
    }
    if (res.status === 403) throw new ApiError("접근이 거절됐습니다. tailscale serve 주소로 열었는지, 허용된 계정인지 확인하세요.", 403);
    let body = null;
    try { body = await res.json(); } catch (_) { body = null; }
    if (!res.ok) throw new ApiError((body && body.error) || `서버 오류 ${res.status}`, res.status);
    return body;
  }

  const allCards = () => list.ai.concat(list.other);
  const findCard = (name) => allCards().find((c) => c.session === name) || null;
  const selCard = () => (sel.session ? findCard(sel.session) : null);
  const selAgent = () => {
    const c = selCard();
    return c ? c.agents.find((a) => a.pane === sel.pane) || null : null;
  };

  // ── 카드 목록 ────────────────────────────────────────────────────────
  const cardsEl = $("cards");
  const ENTER_SVG = '<svg viewBox="0 0 18 18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 4l5 5-5 5"/></svg>';
  let press = null;

  function enterButton(c) {
    const b = el("button", "enter");
    b.type = "button";
    b.setAttribute("aria-label", `${c.session} 세션으로 들어가기`);
    b.innerHTML = ENTER_SVG; // 고정된 아이콘뿐이다
    return b;
  }
  function metaOf(c) {
    const meta = el("div", "meta");
    if (c.branch) meta.append(el("span", "", (c.worktree ? "⌥⌥ " : "⌥ ") + c.branch));
    if (c.agents.length > 1) meta.append(el("span", "", `Claude ${c.agents.length}`));
    return meta.childNodes.length ? meta : null;
  }
  function aiCard(c) {
    const card = el("div", "card");
    card.dataset.session = c.session;
    card.dataset.state = c.state;
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", `${c.session}, ${STATE_LABEL[c.state] || "상태 없음"}, ${c.age}. 누르면 보낼 곳`);
    if (sel.session === c.session) card.classList.add("sel");
    const body = el("div", "body");
    const top = el("div", "top");
    top.append(el("span", "glyph", c.glyph), el("span", "name", c.session));
    if (STATE_LABEL[c.state]) top.append(el("span", "state", STATE_LABEL[c.state]));
    top.append(el("span", "age", c.age));
    body.append(top);
    if (c.state === "approval" && c.reason) body.append(el("p", "reason", c.reason));
    if (c.work) body.append(el("p", "work", c.work));
    const meta = metaOf(c);
    if (meta) body.append(meta);
    card.append(body, enterButton(c));
    return card;
  }
  function plainCard(c) {
    const card = el("div", "card plain");
    card.dataset.session = c.session;
    card.dataset.state = "none";
    card.tabIndex = 0;
    card.setAttribute("role", "button");
    card.setAttribute("aria-label", `${c.session}${c.agents.length ? "" : ", Claude 없음"}`);
    if (sel.session === c.session) card.classList.add("sel");
    const body = el("div", "body");
    const top = el("div", "top");
    top.append(el("span", "name", c.session), el("span", "age", c.age));
    body.append(top);
    if (c.note) body.append(el("p", "note", "✎ " + c.note));
    if (c.agents.length) body.append(el("p", "meta", `Claude ${c.agents.length}`));
    card.append(body, enterButton(c));
    return card;
  }
  function groupHead(label, n) {
    const g = el("div", "group");
    g.append(el("span", "", label), el("span", "", String(n)));
    return g;
  }
  const cardFor = (name) => [...cardsEl.querySelectorAll(".card")].find((c) => c.dataset.session === name);

  let cardsSig = "";
  function renderCards() {
    if (press) return; // 누르는 중에는 카드를 갈아 끼우지 않는다 — 길게 누르기가 끊긴다
    const sig = JSON.stringify([loaded, list, sel.session]);
    if (sig === cardsSig) return; // 같은 목록이면 버튼을 갈아 끼우지 않는다(chips 의 주석)
    cardsSig = sig;
    const focused = document.activeElement && document.activeElement.closest && document.activeElement.closest(".card");
    const keep = focused && cardsEl.contains(focused) ? focused.dataset.session : null;
    const frag = document.createDocumentFragment();
    if (!loaded) {
      frag.append(el("p", "empty", "세션을 불러오는 중…"));
    } else {
      frag.append(groupHead("AI 세션", list.ai.length));
      if (!list.ai.length) frag.append(el("p", "empty", "AI 도구가 도는 세션이 없습니다."));
      list.ai.forEach((c) => frag.append(aiCard(c)));
      if (list.other.length) {
        frag.append(groupHead("다른 세션", list.other.length));
        list.other.forEach((c) => frag.append(plainCard(c)));
      }
    }
    cardsEl.replaceChildren(frag);
    if (keep) cardFor(keep)?.focus({ preventScroll: true });
    $("count-sessions").textContent = loaded ? `세션 ${list.ai.length + list.other.length}` : "";
  }

  // 누르기 = 보낼 곳, › 또는 길게 누르기 = 그 세션에 들어가기
  cardsEl.addEventListener("click", (e) => {
    const b = e.target.closest(".enter");
    if (b) openPage(b.closest(".card").dataset.session);
  });
  cardsEl.addEventListener("pointerdown", (e) => {
    const c = e.target.closest(".card");
    if (!c || e.target.closest(".enter") || e.button > 0) return;
    const session = c.dataset.session;
    c.classList.add("pressing");
    press = {
      c, session, x: e.clientX, y: e.clientY, long: false,
      timer: setTimeout(() => {
        press.long = true;
        c.classList.remove("pressing");
        if (navigator.vibrate) navigator.vibrate(12);
        openPage(session);
      }, LONG_PRESS_MS),
    };
  });
  const cancelPress = () => {
    if (!press) return;
    clearTimeout(press.timer);
    press.c.classList.remove("pressing");
    press = null;
  };
  cardsEl.addEventListener("pointermove", (e) => {
    if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > 8) cancelPress();
  });
  cardsEl.addEventListener("pointerup", () => {
    if (!press) return;
    const { session, long } = press;
    cancelPress();
    if (!long) pickSession(session);
  });
  cardsEl.addEventListener("pointercancel", cancelPress);
  cardsEl.addEventListener("pointerleave", cancelPress);
  cardsEl.addEventListener("contextmenu", (e) => e.preventDefault());
  cardsEl.addEventListener("keydown", (e) => {
    if (e.target.closest(".enter")) return;
    const c = e.target.closest(".card");
    if (c && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      pickSession(c.dataset.session);
    }
  });

  // ── 아래 입력줄 ──────────────────────────────────────────────────────
  const composer = $("composer");
  const prompt = $("prompt");
  const sendBtn = $("sendbtn");
  const resultEl = $("result");
  let hideTimer = 0;
  let sending = false;

  function pickSession(name) {
    const c = findCard(name);
    if (!c) return;
    if (!c.agents.length) {
      notice(`${c.session} 에는 Claude 가 없어 보낼 수 없습니다. › 를 누르면 화면을 봅니다.`);
      return;
    }
    const keep = sel.session === name && c.agents.some((a) => a.pane === sel.pane) ? sel.pane : null;
    // Claude 가 둘 이상이면 조용히 고르지 않는다 — 입력줄에서 고르기 전에는 보내기가 꺼져 있다(agent-api.md).
    sel = { session: name, pane: keep || (c.agents.length === 1 ? c.agents[0].pane : null) };
    hideResult();
    renderTarget();
    renderCards();
  }

  // 칩과 카드는 내용이 바뀔 때만 다시 만든다. 2초마다 버튼을 갈아 끼우면 누르는 사이(pointerdown 과 pointerup 의
  // 대상이 달라져) 클릭이 사라진다 — 데스크톱 Chrome 에서 Claude 고르기 칩이 그렇게 눌리지 않았다(2026-11-17).
  function chips(container, agents, selected, onPick) {
    const sig = JSON.stringify([agents.length < 2 ? [] : agents.map((a) => [a.pane, a.name, a.state]), selected]);
    if (container.dataset.sig === sig) return;
    container.dataset.sig = sig;
    container.replaceChildren();
    if (agents.length < 2) return;
    for (const a of agents) {
      const c = el("button", "chip");
      c.type = "button";
      c.setAttribute("role", "radio");
      c.setAttribute("aria-checked", String(selected === a.pane));
      c.append(el("b", "", `${a.name || "claude"} ${a.pane}`), el("span", "", AGENT_WORD[a.state] || a.state));
      c.addEventListener("click", () => onPick(a.pane));
      container.append(c);
    }
  }

  function renderTarget() {
    const to = $("to");
    to.replaceChildren();
    to.classList.remove("need");
    const n = [...prompt.value].length;
    const c = selCard();
    const a = selAgent();
    if (!sel.session) {
      to.append(el("span", "", "위에서 세션 카드를 눌러 보낼 곳을 고르세요"));
      if (n) to.classList.add("need");
    } else if (!c) {
      to.append(el("span", "", "고른 세션이 목록에 없습니다. 다시 고르세요"));
      to.classList.add("need");
    } else if (answering() && a) {
      // 대화상자의 «직접 쓰기» 를 고른 동안 입력줄은 그 답을 쓰는 칸이다.
      to.append(el("span", "", "답"));
      to.append(el("b", "", `직접 쓰기 · ${a.name || "claude"} ${a.pane}`));
    } else {
      to.append(el("span", "", "보낼 곳"));
      to.append(el("b", "", a ? `${c.agents.length > 1 ? c.session + " · " : ""}${a.name || "claude"} ${a.pane}` : c.session));
      if (a) {
        to.append(el("span", "st", AGENT_WORD[a.state] || a.state));
      } else {
        to.append(el("span", "st", "Claude 를 고르세요"));
        to.classList.add("need");
      }
    }
    if (n) to.append(el("span", "count" + (n > MAX_RUNES ? " over" : ""), `${n}/${MAX_RUNES}`));
    // 세션 페이지에서는 머리에서 고른다 — 같은 고르기를 두 번 그리지 않는다.
    chips($("targets"), c && !page.open ? c.agents : [], sel.pane, (pane) => {
      sel.pane = pane;
      hideResult();
      renderTarget();
    });
    sendBtn.disabled = !a || n === 0 || sending;
    sendBtn.setAttribute("aria-label", answering() ? "답하기" : "보내기");
  }
  const answering = () => page.open && !!page.pending && page.pending.free;

  function grow() {
    prompt.style.height = "auto";
    prompt.style.height = Math.min(prompt.scrollHeight + 2, 132) + "px";
  }
  prompt.addEventListener("input", () => { grow(); renderTarget(); });

  // 서버의 갈래(promptView.result) → 화면 문구. 종료 코드 표는 docs/design/serve.md.
  const RESULTS = {
    sent: { tone: "ok", title: "보냈습니다", body: (a) => `${a.name || "claude"} ${a.pane} 의 입력창에 넣고 제출된 것을 확인했습니다.`, fade: true },
    queued: { tone: "ok", title: "대기열에 넣었습니다", body: () => "지금 턴이 끝나면 이어서 처리합니다.", fade: true },
    busy: { tone: "warn", title: "작업 중입니다", body: () => "지금 턴을 처리하고 있습니다. 대기열에 넣으면 이 턴이 끝난 뒤 처리합니다.", acts: ["queue", "cancel"] },
    blocked: { tone: "stop", title: "답을 기다리고 있습니다", body: () => "Claude 가 질문이나 권한 확인을 띄웠습니다. 먼저 대화상자에 답하세요.", acts: ["dialog"] },
    not_ready: { tone: "stop", title: "지금은 보낼 수 없습니다", body: () => "아무것도 보내지 않았습니다.", why: true, acts: ["screen"] },
    retry: { tone: "warn", title: "제출되지 않았습니다", body: () => "친 글을 입력창에서 지웠습니다. 다시 보내도 됩니다.", acts: ["resend"] },
    unknown: { tone: "stop", title: "제출됐는지 알 수 없습니다", body: () => "화면을 확인하세요. 같은 글을 다시 보내면 두 번 들어갈 수 있습니다.", why: true, acts: ["screen"] },
    not_seen: { tone: "stop", title: "대화 기록에 보이지 않습니다", body: () => "제출은 됐을 수 있습니다. 같은 글을 다시 보내지 말고 화면을 확인하세요.", acts: ["screen"] },
    gone: { tone: "stop", title: "대상 Claude 가 없습니다", body: () => "그 pane 의 Claude 가 끝났거나 다른 Claude 로 바뀌었습니다. 다시 고르세요.", why: true },
    invalid: { tone: "stop", title: "보낼 수 없는 글입니다", body: () => "", why: true },
    error: { tone: "stop", title: "보내지 못했습니다", body: () => "", why: true },
  };
  const ACT_LABEL = { queue: "대기열에 넣기", cancel: "보내지 않기", screen: "화면 보기", resend: "다시 보내기", dialog: "대화상자 보기" };

  function hideResult() {
    clearTimeout(hideTimer);
    resultEl.hidden = true;
  }
  // banner 의 버튼은 결과를 받은 그 Claude(target 의 pane·pid)에게만 동작한다. 그 사이 보낼 곳이 바뀌었으면
  // [대기열에 넣기]·[다시 보내기] 가 다른 Claude 에게 가지 않게 알림을 닫는다.
  function banner(tone, title, body, why, acts, target) {
    clearTimeout(hideTimer);
    resultEl.replaceChildren();
    resultEl.dataset.tone = tone;
    if (title) resultEl.append(el("h3", "", title));
    if (body) resultEl.append(el("p", "", body));
    if (why) resultEl.append(el("p", body ? "why" : "", why));
    if (acts && acts.length) {
      const row = el("div", "acts");
      for (const act of acts) {
        const b = el("button", "btn" + (act === "queue" || act === "resend" ? " primary" : ""), ACT_LABEL[act]);
        b.type = "button";
        b.addEventListener("click", () => {
          const now = selAgent();
          if (target && (!now || now.pane !== target.pane || now.pid !== target.pid)) {
            notice("보낼 곳이 바뀌어 이 알림을 닫았습니다. 보낼 곳을 확인하고 다시 보내세요.");
            return;
          }
          if (act === "queue") send(true);
          else if (act === "resend") send(false);
          else if (act === "dialog" && target) openPage(sel.session, target.pane, "talk");
          else if (act === "screen" && target) openPage(sel.session, target.pane, "screen");
          else if (act === "screen" && sel.session) openPage(sel.session, sel.pane, "screen");
          else hideResult();
        });
        row.append(b);
      }
      resultEl.append(row);
    }
    const x = el("button", "dismiss", "×");
    x.type = "button";
    x.setAttribute("aria-label", "알림 닫기");
    x.addEventListener("click", hideResult);
    resultEl.append(x);
    resultEl.hidden = false;
  }
  function notice(text) {
    banner("info", "", text);
    hideTimer = setTimeout(hideResult, 4000);
  }
  function showResult(key, detail, a) {
    const r = RESULTS[key] || RESULTS.error;
    banner(r.tone, r.title, r.body(a), r.why ? detail : "", r.acts, { pane: a.pane, pid: a.pid });
    if (r.fade) hideTimer = setTimeout(hideResult, 5000);
  }

  async function send(force) {
    const a = selAgent();
    if (!a || sending) return;
    // 서버는 한 줄만 받는다. 줄바꿈은 공백으로 바꾸고 입력칸에도 그 모양을 보여 준다.
    const text = prompt.value.replace(/\s*\n+\s*/g, " ").trim();
    prompt.value = text;
    grow();
    if (!text) { renderTarget(); return; }
    stopListening(false);
    if ([...text].length > MAX_RUNES) {
      banner("stop", RESULTS.invalid.title, `${MAX_RUNES}자를 넘었습니다. 긴 글은 파일에 쓰고 그 경로를 보내세요.`);
      renderTarget();
      return;
    }
    hideResult();
    sending = true;
    sendBtn.classList.add("busy");
    sendBtn.setAttribute("aria-label", "보내는 중");
    renderTarget();
    try {
      const res = await api("/api/prompt", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pane: a.pane, pid: a.pid, text, force: !!force }),
      });
      showResult(res.result, res.detail, a);
      if (res.result === "sent" || res.result === "queued") {
        if (prompt.value === text) prompt.value = "";
        grow();
        page.turnKey = ""; // 대화 탭이 새 턴을 다시 읽게
      }
    } catch (e) {
      if (e.status === 0 || e.status >= 500) {
        // 답을 받지 못했다(폰이 잠김·망이 바뀜·중간 프록시의 시간 초과). 서버는 보내기를 끝까지 하므로 «못 보냈다»
        // 가 아니라 «모른다» 다 — 다시 보내면 두 번 들어갈 수 있다(agent-api.md 의 6 과 7 을 섞지 않는 이유와 같다).
        showResult("unknown", e.message, a);
        page.turnKey = "";
      } else {
        banner("stop", RESULTS.error.title, "", e.message);
      }
    } finally {
      sending = false;
      sendBtn.classList.remove("busy");
      sendBtn.setAttribute("aria-label", "보내기");
      renderTarget();
      schedule(300);
    }
  }
  sendBtn.addEventListener("click", () => {
    if (!answering()) send(false);
    else if (page.pending.multi) answerMulti();
    else answer({});
  });

  // ── 받아쓰기 (SpeechRecognition, ko-KR). 듣는 동안의 글을 입력줄 위에 크게 보이고, 저절로 보내지 않는다 ──
  // 사용자의 폰은 iPhone Safari 다. 그 버릇에 맞춘다(docs/design/serve.md «음성»):
  //   - 한 번 말하기(continuous=false)는 말을 잠깐 멈추면 끝났다(«말하는 도중 끊김») → 계속 듣기로 듣고, 그래도 저절로
  //     끝나면 들은 글을 지닌 채 새 인식기로 이어 듣는다. «그만» 을 누르거나 상한에 닿을 때만 끝난다.
  //   - 멈춘 뒤 앞 글을 지우고 새로 적거나, 앞 결과를 겹쳐 보내거나, 끝에 빈 결과를 보낸다(«말해도 글이 안 들어감») →
  //     결과를 매번 처음부터 다시 엮고(겹친 것은 낱말로 걸러), 앞 글이 사라지면 따로 모아 둔다. 들은 글은 줄지 않는다.
  //   - «그만» 뒤에 끝 신호(onend)가 안 올 때가 있다 → 잠깐 기다렸다 들은 글로 마친다.
  const speechCtor = () => window.SpeechRecognition || window.webkitSpeechRecognition;
  const LISTEN_MAX_MS = 120000; // 한 번 누른 받아쓰기의 상한
  const LISTEN_IDLE_RESTARTS = 5; // 새 글 없이 이어 듣기를 몇 번까지
  const STOP_GRACE_MS = 1500; // «그만» 뒤 끝 신호를 기다리는 시간
  const FATAL_SPEECH = ["not-allowed", "service-not-allowed", "audio-capture", "language-not-supported"];
  const mic = $("mic");
  const listenBox = $("listen");
  const listenText = $("listen-text");
  // listen 은 한 번 누른 받아쓰기다: kept(앞서 마친 글 조각) + cur(지금 인식기의 글), interim(그중 아직 바뀔 수 있는 끝).
  let listen = null;
  if (!speechCtor()) mic.hidden = true; // 폰 키보드의 받아쓰기로 입력칸에 바로 말하면 된다

  // 낱말 비교 — 대소문자·문장부호를 빼고 낱말로 나눈다. 띄어쓰기를 다르게 보낸 같은 말은 같다고 보지 않는다.
  const speechWords = (t) => t.toLowerCase().replace(/[.,!?…"'“”‘’]/g, " ").split(/\s+/).filter(Boolean);
  // extendsWords: longer 가 shorter 를 앞에서부터 그대로 이어 쓴 것인가(같아도 참).
  function extendsWords(longer, shorter) {
    const a = speechWords(longer);
    const b = speechWords(shorter);
    return b.length <= a.length && b.every((w, i) => w === a[i]);
  }
  // joinResults: 결과들을 한 글로 엮는다. Safari 가 앞 결과를 겹쳐 보낸 것(«안녕» 다음 «안녕 하세요»)과 같은 결과를
  // 두 번 보낸 것은 긴 쪽 하나만 남긴다.
  function joinResults(texts) {
    const out = [];
    for (const t of texts.map((x) => x.trim()).filter(Boolean)) {
      const last = out[out.length - 1];
      if (last !== undefined && extendsWords(t, last)) out[out.length - 1] = t;
      else if (last !== undefined && extendsWords(last, t)) continue;
      else out.push(t);
    }
    return out.join(" ");
  }
  const speechLen = (t) => t.replace(/\s+/g, "").length;
  // nextHeard: 지금 인식기의 글 cur 에 새로 엮은 글 next 가 왔을 때 무엇을 남길지. 빈 결과는 버리고, 확정 아닌 결과가
  // 앞부분만 남기면(되감김) 긴 쪽을 지키고, 첫 낱말부터 다른 훨씬 짧은 글이면 Safari 가 멈춘 뒤 새로 적기 시작한
  // 것이라 앞 글을 kept 로 넘긴다.
  function nextHeard(cur, next, final) {
    if (!next) return { cur, done: "" };
    if (!cur || extendsWords(next, cur)) return { cur: next, done: "" };
    if (!final && extendsWords(cur, next)) return { cur, done: "" };
    const restarted = speechWords(next)[0] !== speechWords(cur)[0] && speechLen(next) * 2 < speechLen(cur);
    return restarted ? { cur: next, done: cur } : { cur: next, done: "" };
  }
  const heardText = (l) => [...l.kept, l.cur].filter(Boolean).join(" ").trim();

  function drawHeard() {
    if (!listen) return;
    const all = heardText(listen);
    const dim = listen.interim && all.endsWith(listen.interim) ? listen.interim : "";
    listenText.replaceChildren(document.createTextNode(all.slice(0, all.length - dim.length)));
    // 확정 전의 낱말은 흐리게 — 인식기가 아직 고칠 수 있다.
    if (dim) listenText.append(el("span", "interim", dim));
  }
  function startListening() {
    hideResult();
    listen = { kept: [], cur: "", interim: "", rec: null, started: Date.now(), restarts: 0, idle: 0, stopping: false,
      lastError: "", trace: [], timers: [] };
    mic.classList.add("on");
    mic.setAttribute("aria-label", "그만 듣기");
    listenBox.hidden = false;
    drawHeard();
    listen.timers.push(setTimeout(() => stopListening(true), LISTEN_MAX_MS));
    if (!startRecognizer(listen)) {
      finishListening(false);
      notice("받아쓰기를 시작하지 못했습니다.");
    }
  }
  // startRecognizer 는 새 인식기 하나를 띄운다. 처음 누를 때와, 저절로 끝나 이어 들을 때 부른다.
  function startRecognizer(l) {
    const SR = speechCtor();
    let r;
    try {
      r = new SR();
    } catch (_) {
      return false;
    }
    r.lang = "ko-KR";
    r.interimResults = true;
    r.continuous = true;
    r.maxAlternatives = 1;
    r.heard = false; // 이 인식기가 새 글을 받았나 — 새 글 없이 이어 듣기를 세는 데 쓴다
    const mine = () => listen === l && l.rec === r;
    for (const ev of ["audiostart", "speechstart", "speechend"]) r.addEventListener(ev, () => { if (mine()) l.trace.push(ev); });
    r.onresult = (e) => {
      if (!mine()) return;
      const texts = [];
      for (let i = 0; i < e.results.length; i++) texts.push(e.results[i][0].transcript);
      const lastR = e.results[e.results.length - 1];
      const final = !!lastR && lastR.isFinal;
      const step = nextHeard(l.cur, joinResults(texts), final);
      if (step.done) l.kept.push(step.done);
      if (step.cur !== l.cur) r.heard = true;
      l.cur = step.cur;
      l.interim = !final && lastR ? lastR[0].transcript.trim() : "";
      l.trace.push("result");
      drawHeard();
    };
    r.onerror = (e) => {
      if (!mine()) return;
      l.lastError = e.error;
      l.trace.push("error:" + e.error);
    };
    r.onend = () => { if (mine()) recognizerEnded(l); };
    l.rec = r;
    l.trace.push(l.restarts ? "restart" : "start");
    try {
      r.start();
    } catch (_) {
      l.rec = null;
      return false;
    }
    return true;
  }
  // recognizerEnded: 인식기가 끝났다. «그만» 이 아니고 고칠 수 없는 오류도 아니면 이어 듣는다.
  function recognizerEnded(l) {
    const r = l.rec;
    l.trace.push("end");
    if (l.cur) l.kept.push(l.cur);
    l.cur = "";
    l.interim = "";
    l.rec = null;
    l.idle = r && r.heard ? 0 : l.idle + 1;
    const keepGoing = !l.stopping && !FATAL_SPEECH.includes(l.lastError) && l.idle <= LISTEN_IDLE_RESTARTS &&
      Date.now() - l.started < LISTEN_MAX_MS;
    if (keepGoing) {
      l.restarts++;
      l.lastError = "";
      if (startRecognizer(l)) return;
    }
    finishListening(true);
  }
  function finishListening(commit) {
    const l = listen;
    if (!l) return;
    listen = null;
    for (const t of l.timers) clearTimeout(t);
    if (l.rec) {
      try { l.rec.abort(); } catch (_) { /* 이미 끝났다 */ }
    }
    mic.classList.remove("on");
    mic.setAttribute("aria-label", "말하기");
    listenBox.hidden = true;
    // 멈춘 순간의 중간 결과도 받는다 — iOS 는 그만 누르면 확정 없이 끝날 때가 있다.
    const text = heardText(l);
    const err = l.lastError;
    // 실패 안내는 저절로 접지 않고, 받은 사건 순서를 작게 붙인다 — 폰 화면을 찍어 보내면 무엇이 일어났는지 안다.
    const how = `받은 사건: ${l.trace.slice(-10).join(" → ")}${err ? ` · 오류 ${err}` : ""}`;
    if (commit && text) {
      prompt.value = prompt.value.trim() ? `${prompt.value.trim()} ${text}` : text;
      grow();
      renderTarget();
    } else if (!commit) {
      // 보내기·답하기가 받아쓰기를 거뒀다 — 알릴 것이 없다.
    } else if ((err === "not-allowed" || err === "service-not-allowed") && !l.restarts) {
      banner("stop", "마이크나 받아쓰기를 쓸 수 없습니다",
        "iPhone 은 설정 › 일반 › 키보드 › 받아쓰기 켜기와 이 사이트의 마이크 허용이 필요합니다.", how);
    } else {
      banner("warn", "말을 받아 적지 못했습니다",
        "다시 눌러 말하거나, 입력줄을 누른 뒤 키보드의 마이크로 말하세요.", how);
    }
  }
  // stopListening(true) 는 «그만» — 들은 글을 입력칸에 넣는다. 끝 신호가 안 오면 STOP_GRACE_MS 뒤에 마친다.
  // stopListening(false) 는 거두기(보내기·답하기 때) — 글을 넣지 않는다.
  function stopListening(commit) {
    const l = listen;
    if (!l) return;
    if (!commit) {
      finishListening(false);
      return;
    }
    l.stopping = true;
    l.trace.push("stop");
    if (!l.rec) {
      finishListening(true);
      return;
    }
    try { l.rec.stop(); } catch (_) { /* 이미 끝났다 */ }
    l.timers.push(setTimeout(() => {
      if (listen !== l) return;
      if (l.cur) l.kept.push(l.cur);
      l.cur = "";
      finishListening(true);
    }, STOP_GRACE_MS));
  }
  mic.addEventListener("click", () => (listen ? stopListening(true) : startListening()));

  // ── 세션 페이지 ──────────────────────────────────────────────────────
  // 주소는 #s=<세션> 이다. 폰의 뒤로 가기(iOS 왼쪽 가장자리 밀기 포함)가 목록으로 돌아오고, Safari 가 탭을
  // 다시 불러도 그 세션에 남는다. 우리가 넣은 기록인지는 history.state 가 말한다 — 아니면 back() 을 부르지
  // 않는다(남의 페이지로 물러선다).
  const stack = $("stack");
  const pageEl = $("page");
  const listview = $("listview");

  function routeFromHash() {
    const m = /^#s=(.+)$/.exec(location.hash);
    if (m) showPage(decodeURIComponent(m[1]));
    else hidePage();
  }
  function openPage(name, pane, tab) {
    const c = findCard(name);
    if (c && c.agents.length) {
      const keep = sel.session === name && c.agents.some((a) => a.pane === sel.pane) ? sel.pane : null;
      sel = { session: name, pane: pane || keep || c.agents[0].pane };
    }
    page.tab = tab || "talk";
    page.turnKey = "";
    const hash = "#s=" + encodeURIComponent(name);
    if (location.hash !== hash) history.pushState({ muxPage: true }, "", hash);
    showPage(name);
  }
  function closePage() {
    if (!page.open) return;
    if (history.state && history.state.muxPage) {
      history.back(); // popstate 가 목록으로 돌린다
      return;
    }
    history.replaceState(null, "", location.pathname + location.search);
    hidePage();
  }
  window.addEventListener("popstate", routeFromHash);
  $("page-back").addEventListener("click", closePage);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && page.open) closePage(); });

  function showPage(name) {
    const same = page.open && page.session === name;
    page.open = true;
    page.session = name;
    if (!same) {
      page.stick = true;
      page.turn = null;
      page.turnFor = "";
      page.talkSig = "";
      clearDialog();
      $("screen-box").replaceChildren();
    }
    pageEl.hidden = false;
    stack.classList.add("deep");
    listview.inert = true;
    hideResult();
    renderPage();
    renderTarget();
    refreshPage();
    // 페이지 자체로 포커스를 옮긴다 — 버튼에 주면 손가락으로 연 폰에도 포커스 테두리가 그려진다.
    if (!same) pageEl.focus({ preventScroll: true });
  }
  function hidePage() {
    if (!page.open) return;
    const name = page.session;
    page.open = false;
    pageEl.hidden = true;
    stack.classList.remove("deep");
    listview.inert = false;
    composer.hidden = false;
    renderCards();
    renderTarget();
    cardFor(name)?.focus({ preventScroll: true });
  }

  function renderPage() {
    const name = page.session;
    const c = findCard(name);
    const head = $("page-head");
    $("p-name").textContent = name;
    if (!c) {
      head.dataset.state = "none";
      $("p-glyph").textContent = "";
      $("p-state").textContent = "";
      $("p-age").textContent = "";
      $("p-meta").textContent = loaded ? "이 세션이 목록에 없습니다. 닫혔거나 이름이 바뀌었습니다." : "불러오는 중…";
      chips($("page-targets"), [], null, () => {}); // 바뀐 것만 그리는 표시(dataset.sig)도 같이 비운다
      $("tab-talk").hidden = true;
      $("panel-talk").hidden = true;
      $("panel-screen").hidden = true;
      composer.hidden = true;
      return;
    }
    // 들어간 세션이 곧 보낼 곳이다. 들어갈 때(다른 세션이 골라져 있었거나 아직 Claude 를 안 골랐을 때)만 여기서
    // 정한다. 고른 Claude 가 이번 목록에 안 보여도 다른 Claude 로 바꾸지 않는다 — 상태 파일을 덮어쓰는 순간 읽으면
    // 한 번 빠질 수 있고(tmux/agent.go 의 찢어진 읽기), 바꾸면 그 뒤의 보내기가 사람 몰래 다른 Claude 에게 간다.
    if (c.agents.length && (sel.session !== name || !sel.pane)) {
      sel = { session: name, pane: c.agents[0].pane };
    }
    head.dataset.state = c.state;
    $("p-glyph").textContent = c.glyph || "";
    $("p-state").textContent = STATE_LABEL[c.state] || (c.agents.length ? "" : "Claude 없음");
    $("p-age").textContent = c.age;
    $("p-meta").textContent = [c.branch ? (c.worktree ? "⌥⌥ " : "⌥ ") + c.branch : "", c.dir || ""].filter(Boolean).join(" · ");
    chips($("page-targets"), c.agents, sel.pane, (pane) => {
      sel.pane = pane;
      page.stick = true;
      page.turnKey = "";
      clearDialog(); // 고르던 선택지는 앞 Claude 의 대화상자 것이다
      hideResult();
      renderPage();
      renderTarget();
      refreshPage();
    });
    const hasTalk = c.agents.length > 0;
    composer.hidden = !hasTalk;
    $("tab-talk").hidden = !hasTalk;
    const onTalk = page.tab === "talk" && hasTalk;
    $("tab-talk").setAttribute("aria-selected", String(onTalk));
    $("tab-screen").setAttribute("aria-selected", String(!onTalk));
    $("panel-talk").hidden = !onTalk;
    $("panel-screen").hidden = onTalk;
    if (onTalk) renderTalk(c, selAgent());
  }
  $("tab-talk").addEventListener("click", () => {
    page.tab = "talk";
    page.turnKey = "";
    renderPage();
    refreshPage();
  });
  $("tab-screen").addEventListener("click", () => {
    page.tab = "screen";
    page.stick = true;
    renderPage();
    refreshPage();
  });

  // refreshPage 는 지금 탭의 자료를 가져온다. 대화는 그 Claude 의 (상태, 시작 시각) 이 바뀔 때만 다시 읽는다 —
  // 기록은 수 MB 일 수 있고, 회신 규칙상 턴이 끝나야 달라진다. 화면은 열려 있는 동안 매번 캡처한다.
  function refreshPage() {
    if (!page.open || document.hidden) return;
    const c = findCard(page.session);
    if (!c) return;
    const a = c.agents.length ? selAgent() : null;
    if (page.tab === "talk" && a) {
      const key = `${a.pane}|${a.pid}|${a.state}|${a.since || 0}`;
      if (key !== page.turnKey) fetchTurn(a, key);
      // 막혀 있는 동안은 갱신마다 대화상자를 읽는다 — 질문이 여럿이면 답할 때마다 다음 질문으로 바뀐다.
      if (a.state === "blocked") fetchDialog(a);
      else if (page.dialog) clearDialog();
    } else if (a || !c.agents.length) {
      // Claude 가 있는 세션인데 고른 Claude 가 안 보이면 캡처하지 않는다 — 세션의 활성 pane 은 다른 pane 일 수 있다.
      fetchScreen(c, a);
    }
  }

  let turnBusy = false;
  async function fetchTurn(a, key) {
    if (turnBusy) return;
    turnBusy = true;
    page.turnKey = key;
    let t;
    try {
      t = await api(`/api/turn?pane=${encodeURIComponent(a.pane)}&pid=${a.pid}`);
    } catch (e) {
      t = { result: "error", detail: e.message };
      page.turnKey = ""; // 다음 갱신에 다시 읽는다
    } finally {
      turnBusy = false;
    }
    if (sel.pane !== a.pane) {
      page.turnKey = ""; // 읽는 사이 다른 Claude 를 골랐다 — 버리고 다시 읽는다
      return;
    }
    page.turn = t;
    page.turnFor = a.pane;
    if (page.open) renderPage();
  }

  // 대화 탭은 내용이 바뀔 때만 다시 그린다. 2초마다 갈아 끼우면 대화상자의 선택지 버튼이 누르는 사이 바뀌어 클릭이
  // 사라지고(칩과 같은 문제), 회신 글을 손가락으로 골라 복사할 수도 없다.
  function renderTalk(c, a) {
    const panel = $("panel-talk");
    const t = a && page.turnFor === a.pane ? page.turn : null;
    const dlg = a && a.state === "blocked" && page.dialogFor === a.pane ? page.dialog : null;
    const sig = JSON.stringify([a && a.pane, a && a.state, t, dlg, page.pending, page.multi, page.confirmCancel, sending,
      a && a.state === "working" ? c.age : "", c.work, c.reason, c.agents.length]);
    if (sig === page.talkSig) return;
    page.talkSig = sig;

    const out = [el("p", "turn-note", "마지막 턴")];
    if (!a) {
      // 고른 Claude 가 이번 목록에 없다. 잠깐 빠진 것이면 다음 갱신에 돌아온다. 보내기는 그동안 꺼져 있다.
      out.push(el("p", "muted", `고른 Claude(${sel.pane || "?"})가 지금 목록에 없습니다. 잠깐 빠진 것이면 곧 돌아옵니다. 끝났다면 다른 Claude 를 고르세요.`));
      if (c.agents.length === 1) {
        // Claude 가 하나면 머리에 고르기 칩이 없다 — 여기서 고르게 한다.
        const only = c.agents[0];
        out.push(button("btn", `${only.name || "claude"} ${only.pane} 로 고르기`, () => {
          sel.pane = only.pane;
          page.turnKey = "";
          hideResult();
          renderPage();
          renderTarget();
          refreshPage();
        }));
      }
      panel.replaceChildren(...out);
      return;
    }
    if (t && t.prompt) {
      const me = el("div", "me");
      const who = t.human ? "보낸 말" : "사람이 연 턴이 아님 (작업 알림 등)";
      me.append(el("span", "lbl", who + (t.prompt.at ? ` · ${hhmm(t.prompt.at)}` : "")), el("div", t.human ? "bubble" : "bubble other", t.prompt.text));
      out.push(me);
    }
    if (a.state === "blocked") {
      // 답을 기다리는 대화상자 — 대화 기록이 아니라 화면에서 읽은 것이다.
      out.push(dialogBox(c, a));
    } else if (!t) {
      out.push(el("p", "muted", "대화 기록을 읽는 중…"));
    } else if (t.result === "ok" && t.reply) {
      const cl = el("div", "cl");
      cl.append(el("span", "lbl", `Claude 회신${t.reply.at ? " · " + hhmm(t.reply.at) : ""} · 대화 기록에서 읽음`), el("p", "txt", t.reply.text));
      out.push(cl);
    } else if (t.result === "running" || a.state === "working") {
      const w = el("div", "wait");
      const row = el("div", "row1");
      row.append(el("i"), el("span", "", `작업 중 · ${c.age}`));
      w.append(row);
      // 세션의 «지금 하는 일» 은 세션의 Claude 하나의 것이다 — Claude 가 둘이면 어느 쪽 것인지 모른다.
      if (c.work && c.agents.length === 1) w.append(el("p", "now", c.work));
      w.append(el("p", "muted", "끝나면 회신이 여기에 나옵니다."));
      out.push(w);
    } else if (t.result === "no_reply") {
      out.push(el("p", "muted", "마지막 턴에 회신 글이 없습니다."));
    } else if (t.result === "gone") {
      out.push(el("p", "muted", "이 Claude 가 끝났거나 바뀌었습니다. 목록으로 돌아가 다시 고르세요."));
    } else if (t.result !== "blocked") {
      out.push(el("p", "muted", `대화를 읽지 못했습니다. ${t.detail || ""}`));
    }
    panel.replaceChildren(...out);
  }

  function button(cls, text, onClick) {
    const b = el("button", cls, text);
    b.type = "button";
    b.addEventListener("click", onClick);
    return b;
  }
  function toScreenTab() {
    page.tab = "screen";
    page.stick = true;
    renderPage();
    refreshPage();
  }

  // ── 대화상자에 답하기 (질문·권한 확인). 선택지를 누르면 확인을 한 번 더 받고, 서버는 누르기 직전에 화면을 다시
  // 읽어 폰이 본 선택지(번호와 글)일 때만 숫자키를 누른다(tmux/agent_dialog.go). ──
  const DIALOG_KIND = { question: "질문", permission: "권한 확인", other: "대화상자" };
  const shorten = (s, n) => ([...s].length > n ? [...s].slice(0, n - 1).join("") + "…" : s);

  function dialogBox(c, a) {
    const box = el("div", "dlg");
    const r = page.dialogFor === a.pane ? page.dialog : null;
    if (!r) {
      box.append(el("p", "muted", "대화상자를 읽는 중…"));
      return box;
    }
    if (r.result !== "ok") {
      box.append(el("p", "dlg-kind", c.reason || "승인 대기"));
      box.append(el("p", "", r.result === "none"
        ? "번호로 고를 수 있는 대화상자가 아닙니다. 화면 탭에서 확인하세요."
        : `대화상자를 읽지 못했습니다. ${r.detail || ""}`));
      box.append(button("btn", "화면 보기", toScreenTab));
      return box;
    }
    const d = r.dialog;
    const questions = d.tabs.filter((tab) => !tab.submit);
    let kind = DIALOG_KIND[d.kind] || "대화상자";
    if (questions.length > 1) {
      const at = questions.findIndex((tab) => !tab.done);
      kind += at < 0 ? " · 검토하고 제출" : ` ${at + 1}/${questions.length} · ${questions[at].label}`;
    }
    if (d.multi) kind += " · 여러 개 고르기";
    box.append(el("p", "dlg-kind", kind));
    if (d.tabs.length) {
      const tabs = el("div", "dlg-tabs");
      for (const tab of d.tabs) tabs.append(el("span", "dlg-tab" + (tab.done ? " done" : "") + (tab.submit ? " submit" : ""), (tab.done ? "☒ " : tab.submit ? "✔ " : "☐ ") + tab.label));
      box.append(tabs);
    }
    if (d.prompt.length) box.append(el("div", "dlg-prompt" + (d.kind === "permission" ? " mono" : ""), d.prompt.join("\n")));

    if (d.multi) {
      box.append(multiOptions(d));
    } else {
      const opts = el("div", "dlg-opts");
      for (const o of d.options) {
        const picked = page.pending && page.pending.n === o.n;
        const b = el("button", "opt" + (picked ? " picked" : ""));
        b.type = "button";
        b.setAttribute("aria-pressed", String(picked));
        const head = el("span", "ohead");
        head.append(el("span", "onum", String(o.n)), el("span", "olabel", o.freeText ? "직접 쓰기" : o.label));
        if (o.chosen) head.append(el("span", "ochosen", "✔ 앞서 고름"));
        b.append(head);
        const detail = o.freeText
          ? (o.draft ? `칸에 쓰다 만 글 «${shorten(o.draft, 40)}» — 고르면 지우고 새로 씁니다` : "아래 입력줄에 쓰거나 말해서 보냅니다")
          : o.detail;
        if (detail) b.append(el("span", "odetail", detail));
        b.addEventListener("click", () => {
          page.pending = { n: o.n, label: o.label, free: o.freeText, key: d.key };
          page.confirmCancel = "";
          hideResult();
          renderPage();
          renderTarget();
        });
        opts.append(b);
      }
      box.append(opts);

      if (page.pending) {
        const bar = el("div", "dlg-confirm");
        if (page.pending.free) {
          bar.append(el("p", "", "아래 입력줄에 답을 쓰거나 말한 뒤 ↑ 를 누르세요."));
        } else {
          bar.append(button("btn primary", sending ? "보내는 중…" : `«${shorten(page.pending.label, 28)}» 로 답하기`, () => {
            if (page.pending) answer({ key: page.pending.key, n: page.pending.n, label: page.pending.label });
          }));
        }
        bar.append(button("btn", "다시 고르기", () => {
          page.pending = null;
          renderPage();
          renderTarget();
        }));
        box.append(bar);
      }
    }
    const cancel = el("div", "dlg-cancel");
    if (!page.confirmCancel) {
      cancel.append(button("linkish", "답하지 않고 닫기 (Esc)", () => {
        page.confirmCancel = d.key;
        page.pending = null;
        renderPage();
        renderTarget();
      }));
    } else {
      cancel.append(el("p", "", d.kind === "permission" ? "닫으면 이 실행을 거절합니다." : "닫으면 이 질문에 답하지 않은 것이 됩니다."));
      cancel.append(button("btn stop", "닫기", () => answer({ key: page.confirmCancel, cancel: true })), button("btn", "아니요", () => {
        page.confirmCancel = "";
        renderPage();
      }));
    }
    box.append(cancel);
    return box;
  }

  // 여러 개 고르기 — 체크박스는 폰 안에서만 켜고 끄고(page.multi), «다음» 을 누르면 한 번에 보낸다. 서버는 다른 체크만
  // 숫자키로 맞추고, 직접 쓰기 글은 화살표로 그 칸에 가서 치고, Submit 줄에서 Enter 한다(tmux.AnswerMulti). 그다음
  // 뜨는 Submit 탭(검토)은 한 개 고르기처럼 확인을 한 번 더 받고 제출한다.
  function multiBoxes(d) {
    return d.options.filter((o) => o.checkbox);
  }
  function multiOptions(d) {
    const boxes = multiBoxes(d);
    if (!page.multi || page.multi.key !== d.key) {
      page.multi = { key: d.key, checked: Object.fromEntries(boxes.map((o) => [o.n, o.checked])) };
    }
    const wrap = el("div", "dlg-opts");
    for (const o of boxes) {
      const on = !!page.multi.checked[o.n];
      const b = el("button", "opt check" + (on ? " picked" : ""));
      b.type = "button";
      b.setAttribute("role", "checkbox");
      b.setAttribute("aria-checked", String(on));
      const head = el("span", "ohead");
      head.append(el("span", "obox", on ? "✓" : ""), el("span", "olabel", o.freeText ? "직접 쓰기" : o.label));
      b.append(head);
      const detail = o.freeText
        ? (o.draft ? `칸에 쓰다 만 글 «${shorten(o.draft, 40)}» — 켜면 지우고 아래 입력줄의 글로 바꿉니다` : "켜면 아래 입력줄에 쓴 글을 답으로 더합니다")
        : o.detail;
      if (detail) b.append(el("span", "odetail", detail));
      b.addEventListener("click", () => {
        page.multi.checked[o.n] = !on;
        if (o.freeText) page.pending = !on ? { n: o.n, label: o.label, free: true, multi: true, key: d.key } : null;
        page.confirmCancel = "";
        hideResult();
        renderPage();
        renderTarget();
      });
      wrap.append(b);
    }
    const count = boxes.filter((o) => page.multi.checked[o.n]).length;
    const bar = el("div", "dlg-confirm");
    if (page.pending && page.pending.multi) bar.append(el("p", "", "직접 쓰기 글은 아래 입력줄에 쓰거나 말하세요."));
    const next = button("btn primary", sending ? "보내는 중…" : `고른 ${count}개로 다음 →`, () => answerMulti());
    next.disabled = count === 0 || sending;
    bar.append(next);
    const out = el("div", "dlg-multi");
    out.append(wrap, bar);
    return out;
  }
  function answerMulti() {
    const d = page.dialog && page.dialog.dialog;
    if (!d || !d.multi || !page.multi) return;
    const boxes = multiBoxes(d);
    // 그사이 다른 대화상자로 바뀌었으면 고른 체크는 그 대화상자의 것이 아니다 — 보내지 않는다.
    if (page.multi.key !== d.key) return;
    const options = boxes.map((o) => ({ n: o.n, label: o.label, checked: !!page.multi.checked[o.n] }));
    if (!options.some((o) => o.checked)) return;
    const free = boxes.find((o) => o.freeText && page.multi.checked[o.n]);
    let text = "";
    if (free) {
      text = prompt.value.replace(/\s*\n+\s*/g, " ").trim();
      prompt.value = text;
      grow();
      if (!text) {
        banner("warn", "직접 쓰기 글이 없습니다", "아래 입력줄에 쓰거나 말한 뒤 다시 누르세요. 직접 쓰기를 끄면 글 없이 보냅니다.");
        return;
      }
    }
    answer({ key: page.multi.key, multi: true, options, text });
  }

  let dialogBusy = false;
  async function fetchDialog(a) {
    if (dialogBusy) return;
    dialogBusy = true;
    let r;
    try {
      r = await api(`/api/dialog?pane=${encodeURIComponent(a.pane)}&pid=${a.pid}`);
    } catch (e) {
      r = { result: "error", detail: e.message };
    } finally {
      dialogBusy = false;
    }
    if (sel.pane !== a.pane || !page.open) return;
    const sig = JSON.stringify(r);
    if (sig === page.dialogSig) return;
    page.dialogSig = sig;
    page.dialog = r;
    page.dialogFor = a.pane;
    // 다른 대화상자가 됐으면(열쇠가 다르다) 고르던 선택지·Esc 확인·체크를 모두 푼다 — 번호와 글이 같아도 다른
    // 대화상자일 수 있다(권한 확인은 어느 것이나 «1. Yes»). 서버도 열쇠를 다시 보지만, 폰이 먼저 보여 주지 않는다.
    const key = r.result === "ok" ? r.dialog.key : "";
    if (page.pending && page.pending.key !== key) page.pending = null;
    if (page.confirmCancel && page.confirmCancel !== key) page.confirmCancel = "";
    if (page.multi && page.multi.key !== key) page.multi = null;
    renderPage();
    renderTarget();
  }
  function forgetChoice() {
    page.pending = null;
    page.multi = null;
    page.confirmCancel = "";
  }
  function clearDialog() {
    page.dialog = null;
    page.dialogFor = "";
    page.dialogSig = "";
    page.pending = null;
    page.multi = null;
    page.confirmCancel = "";
  }

  const ANSWER_RESULTS = {
    answered: { tone: "ok", title: "답했습니다", body: () => "Claude 가 다음으로 넘어갑니다.", fade: true },
    cancelled: { tone: "ok", title: "대화상자를 닫았습니다", body: () => "", fade: true },
    changed: { tone: "warn", title: "대화상자가 바뀌었습니다", body: () => "보던 화면과 지금 화면이 달라 누르지 않았습니다. 새 화면을 보고 다시 고르세요." },
    none: { tone: "info", title: "대화상자가 이미 닫혔습니다", body: () => "" },
    unknown: { tone: "stop", title: "답이 들어갔는지 알 수 없습니다", body: () => "화면 탭에서 확인하세요. 같은 답을 다시 누르면 다음 질문에 들어갈 수 있습니다.", why: true, acts: ["screen"] },
    not_ready: { tone: "stop", title: "지금은 답할 수 없습니다", body: () => "아무것도 누르지 않았습니다.", why: true, acts: ["screen"] },
    gone: { tone: "stop", title: "대상 Claude 가 없습니다", body: () => "", why: true },
    invalid: { tone: "stop", title: "보낼 수 없는 글입니다", body: () => "", why: true },
    error: { tone: "stop", title: "답하지 못했습니다", body: () => "", why: true },
  };

  async function answer(body) {
    const a = selAgent();
    if (!a || sending) return;
    let text;
    if (page.pending && page.pending.free && !page.pending.multi && !body.cancel && !body.multi) {
      text = prompt.value.replace(/\s*\n+\s*/g, " ").trim();
      prompt.value = text;
      grow();
      if (!text) { renderTarget(); return; }
      if ([...text].length > MAX_RUNES) {
        banner("stop", ANSWER_RESULTS.invalid.title, `${MAX_RUNES}자를 넘었습니다.`);
        return;
      }
      body = { key: page.pending.key, n: page.pending.n, label: page.pending.label, text };
    }
    stopListening(false);
    hideResult();
    sending = true;
    sendBtn.classList.add("busy");
    renderPage();
    renderTarget();
    try {
      const res = await api("/api/answer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pane: a.pane, pid: a.pid, ...body }),
      });
      const r = ANSWER_RESULTS[res.result] || ANSWER_RESULTS.error;
      banner(r.tone, r.title, r.body(), r.why ? res.detail : "", r.acts, { pane: a.pane, pid: a.pid });
      if (r.fade) hideTimer = setTimeout(hideResult, 4000);
      // 보낼 수 없는 글(invalid)만 고르기를 남겨 글을 고쳐 다시 보내게 한다. 나머지는 모두 푼다 — 들어갔는지 모를
      // 때(unknown) 같은 고르기를 남기면, 다음에 뜬 대화상자에 같은 답을 다시 누르기 쉽다.
      if (res.result !== "invalid") forgetChoice();
      if (res.result === "answered") {
        const sentText = text || body.text;
        if (sentText && prompt.value === sentText) { prompt.value = ""; grow(); }
      }
    } catch (e) {
      forgetChoice();
      if (e.status === 0 || e.status >= 500) {
        const r = ANSWER_RESULTS.unknown;
        banner(r.tone, r.title, r.body(), e.message, r.acts, { pane: a.pane, pid: a.pid });
      } else {
        banner("stop", ANSWER_RESULTS.error.title, "", e.message);
      }
    } finally {
      sending = false;
      sendBtn.classList.remove("busy");
      page.dialogSig = ""; // 넘어간 다음 화면을 바로 다시 읽는다
      page.turnKey = "";
      renderPage();
      renderTarget();
      schedule(300);
    }
  }

  let screenBusy = false;
  async function fetchScreen(c, a) {
    if (screenBusy) return;
    screenBusy = true;
    const q = a ? `pane=${encodeURIComponent(a.pane)}&pid=${a.pid}` : `session=${encodeURIComponent(c.session)}`;
    try {
      const s = await api(`/api/screen?${q}`);
      // 읽는 사이 페이지를 닫았거나 대화 탭으로 옮겼으면 그리지 않는다.
      if (page.open && page.session === c.session && (page.tab === "screen" || !c.agents.length)) drawScreen(s.ansi || "");
    } catch (e) {
      if (page.open) $("screen-box").replaceChildren(el("p", "muted", e.message));
    } finally {
      screenBusy = false;
    }
  }
  // 맨 아래를 보고 있으면 따라 내려가고, 위로 올려 읽는 중이면 그 자리에 둔다. 가로 위치는 늘 그대로.
  function drawScreen(text) {
    const box = $("screen-box");
    const atBottom = box.scrollTop + box.clientHeight >= box.scrollHeight - 6;
    const left = box.scrollLeft;
    box.replaceChildren(...text.replace(/\n+$/, "").split("\n").map(ansiLine));
    box.scrollLeft = left;
    if (page.stick || atBottom) requestAnimationFrame(() => { box.scrollTop = box.scrollHeight; });
    page.stick = false;
  }

  // ── 화면 탭의 ANSI → DOM. SGR 만 읽고 나머지 이스케이프(OSC 8 등)는 버린다 ──────────
  // 넓은 글자의 범위 표는 Go 의 ansi.StringWidth 를 흉내 낸 것이다. 어긋나면 그 줄의 정렬만 틀린다.
  const WIDE = [[0x1100, 0x115F], [0x231A, 0x231B], [0x23E9, 0x23EC], [0x23F0, 0x23F0], [0x23F3, 0x23F3], [0x25FD, 0x25FE],
    [0x2614, 0x2615], [0x2648, 0x2653], [0x267F, 0x267F], [0x2693, 0x2693], [0x26A1, 0x26A1], [0x26AA, 0x26AB], [0x26BD, 0x26BE],
    [0x26C4, 0x26C5], [0x26CE, 0x26CE], [0x26D4, 0x26D4], [0x26EA, 0x26EA], [0x26F2, 0x26F3], [0x26F5, 0x26F5], [0x26FA, 0x26FA],
    [0x26FD, 0x26FD], [0x2705, 0x2705], [0x270A, 0x270B], [0x2728, 0x2728], [0x274C, 0x274C], [0x274E, 0x274E], [0x2753, 0x2755],
    [0x2757, 0x2757], [0x2795, 0x2797], [0x27B0, 0x27B0], [0x27BF, 0x27BF], [0x2B1B, 0x2B1C], [0x2B50, 0x2B50], [0x2B55, 0x2B55],
    [0x2E80, 0x303E], [0x3041, 0x33FF], [0x3400, 0x4DBF], [0x4E00, 0x9FFF], [0xA000, 0xA4CF], [0xAC00, 0xD7A3], [0xF900, 0xFAFF],
    [0xFE30, 0xFE4F], [0xFF00, 0xFF60], [0xFFE0, 0xFFE6], [0x1F300, 0x1F64F], [0x1F680, 0x1F6FF], [0x1F900, 0x1F9FF],
    [0x20000, 0x3FFFD]];
  const cellWidth = (cp) => (cp < 0x1100 ? 1 : WIDE.some(([a, b]) => cp >= a && cp <= b) ? 2 : 1);
  const xterm256 = (n) => {
    if (n < 16) return `var(--a${n})`;
    if (n >= 232) {
      const g = 8 + (n - 232) * 10;
      return `rgb(${g},${g},${g})`;
    }
    n -= 16;
    const lv = (v) => (v ? 55 + v * 40 : 0);
    return `rgb(${lv(Math.floor(n / 36))},${lv(Math.floor(n / 6) % 6)},${lv(n % 6)})`;
  };
  const RESET = { fg: null, bg: null, bold: false, dim: false, italic: false, ul: false, rev: false };
  function applySgr(st, params) {
    const p = params === "" ? [0] : params.split(";").map(Number);
    for (let i = 0; i < p.length; i++) {
      const c = p[i];
      if (c === 0) Object.assign(st, RESET);
      else if (c === 1) st.bold = true;
      else if (c === 2) st.dim = true;
      else if (c === 3) st.italic = true;
      else if (c === 4) st.ul = true;
      else if (c === 7) st.rev = true;
      else if (c === 22) { st.bold = false; st.dim = false; }
      else if (c === 23) st.italic = false;
      else if (c === 24) st.ul = false;
      else if (c === 27) st.rev = false;
      else if (c >= 30 && c <= 37) st.fg = `var(--a${c - 30})`;
      else if (c >= 90 && c <= 97) st.fg = `var(--a${c - 82})`;
      else if (c === 39) st.fg = null;
      else if (c >= 40 && c <= 47) st.bg = `var(--a${c - 40})`;
      else if (c >= 100 && c <= 107) st.bg = `var(--a${c - 92})`;
      else if (c === 49) st.bg = null;
      else if ((c === 38 || c === 48) && p[i + 1] === 5) { st[c === 38 ? "fg" : "bg"] = xterm256(p[i + 2] | 0); i += 2; }
      else if ((c === 38 || c === 48) && p[i + 1] === 2) { st[c === 38 ? "fg" : "bg"] = `rgb(${p[i + 2] | 0},${p[i + 3] | 0},${p[i + 4] | 0})`; i += 4; }
    }
  }
  function appendCells(target, text) {
    let run = "";
    for (const ch of text) {
      const cp = ch.codePointAt(0);
      if (cp < 0x80) { run += ch; continue; }
      if (cp === 0xFE0F || cp === 0x200D) continue;
      if (run) { target.append(run); run = ""; }
      target.append(el("span", cellWidth(cp) === 2 ? "w2" : "w1", ch));
    }
    if (run) target.append(run);
  }
  function emit(parent, text, st) {
    if (!text) return;
    const plain = !st.fg && !st.bg && !st.bold && !st.dim && !st.italic && !st.ul && !st.rev;
    const span = plain ? parent : document.createElement("span");
    if (!plain) {
      let fg = st.fg;
      let bg = st.bg;
      if (st.rev) {
        const f = fg || "var(--t-fg)";
        fg = bg || "var(--t-bg)";
        bg = f;
      }
      // CSSOM 으로 칠한다 — CSP 의 style-src 'self' 는 style 속성 글을 막지만 이것은 막지 않는다.
      if (fg) span.style.color = fg;
      if (bg) span.style.backgroundColor = bg;
      if (st.bold) span.style.fontWeight = "700";
      if (st.dim) span.style.opacity = "0.62";
      if (st.italic) span.style.fontStyle = "italic";
      if (st.ul) span.style.textDecoration = "underline";
    }
    appendCells(span, text);
    if (!plain) parent.append(span);
  }
  const ESC_RE = /\x1b(?:\[([0-9;?]*)([A-Za-z])|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][0-9A-Za-z]|[=>78])/g;
  function ansiLine(text) {
    const div = el("div", "ln");
    const st = { ...RESET };
    let last = 0;
    for (const m of text.matchAll(ESC_RE)) {
      emit(div, text.slice(last, m.index), st);
      if (m[2] === "m" && !(m[1] || "").includes("?")) applySgr(st, m[1] || "");
      last = m.index + m[0].length;
    }
    emit(div, text.slice(last), st);
    if (!div.childNodes.length) div.textContent = " ";
    return div;
  }

  // ── 2초 폴링. 페이지가 가려지면 멈추고, 다시 보이면 바로 한 번 ───────────────────
  const live = $("live");
  const offline = $("offline");
  let pollTimer = 0;
  let polling = false;
  function schedule(ms) {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, ms);
  }
  async function poll() {
    if (polling || document.hidden) return;
    polling = true;
    try {
      list = await api("/api/list");
      loaded = true;
      offline.hidden = true;
      live.classList.remove("down");
      $("live-text").textContent = `${clock(Date.now())} 갱신`;
    } catch (e) {
      offline.textContent = e.message;
      offline.hidden = false;
      live.classList.add("down");
      $("live-text").textContent = "연결 끊김";
    } finally {
      polling = false;
    }
    live.classList.add("tick");
    setTimeout(() => live.classList.remove("tick"), 300);
    renderCards();
    if (page.open) {
      renderPage(); // 들어간 세션의 보낼 곳을 정할 수 있다 — 입력줄보다 먼저
      refreshPage();
    }
    renderTarget();
    schedule(POLL_MS);
  }
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) clearTimeout(pollTimer);
    else schedule(0);
  });

  routeFromHash();
  renderCards();
  renderTarget();
  poll();
})();
