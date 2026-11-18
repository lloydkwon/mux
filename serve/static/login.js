// Muxtap 로그인 화면(docs/design/serve.md «패스키 로그인»). 서버는 갈래(result)만 내고, 문구는 여기서 정한다.
// 등록은 데스크톱의 `mux serve enroll` 이 낸 코드가 있어야 하고, 로그인은 등록한 패스키(Face ID·보안 키)로 한다.
"use strict";
(() => {
  const $ = (id) => document.getElementById(id);
  const msg = $("msg");
  const unlockBox = $("unlock-box");
  const enrollBox = $("enroll-box");
  const showEnroll = $("show-enroll");
  const unlockBtn = $("unlock");
  const registerBtn = $("register");
  const codeInput = $("code");

  const RESULTS = {
    none: "등록된 패스키가 없습니다. 이 기기를 먼저 등록하세요.",
    stale: "시간이 지났습니다. 다시 누르세요.",
    failed: "확인하지 못했습니다. 다시 누르세요. 계속 안 되면 서버 로그(journalctl --user -u mux-serve)를 보세요.",
    broken: "서버가 패스키 파일을 읽거나 쓰지 못했습니다. 서버 로그를 보세요.",
    nocode: "쓸 수 있는 등록 코드가 없습니다. 데스크톱에서 mux serve enroll 을 실행하세요.",
    expired: "코드의 시간이 지났거나 이미 쓰였습니다. 데스크톱에서 새 코드를 받으세요.",
    badcode: "코드가 틀렸습니다. 다시 확인하세요.",
    locked: "코드를 너무 많이 틀려 서버가 지웠습니다. 데스크톱에서 새 코드를 받으세요.",
  };

  function say(text, tone) {
    msg.textContent = text;
    msg.dataset.tone = tone || "";
  }

  // ── base64url ↔ ArrayBuffer (WebAuthn 의 바이트 자리) ─────────────────
  function toBuf(s) {
    let b = s.replace(/-/g, "+").replace(/_/g, "/");
    while (b.length % 4) b += "=";
    const bin = atob(b);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }
  function fromBuf(buf) {
    const bytes = new Uint8Array(buf);
    let bin = "";
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  }
  const descriptors = (list) => (list || []).map((c) => ({ ...c, id: toBuf(c.id) }));

  function creationOptions(o) {
    const pk = o.publicKey;
    return { ...pk, challenge: toBuf(pk.challenge), user: { ...pk.user, id: toBuf(pk.user.id) }, excludeCredentials: descriptors(pk.excludeCredentials) };
  }
  function requestOptions(o) {
    const pk = o.publicKey;
    return { ...pk, challenge: toBuf(pk.challenge), allowCredentials: descriptors(pk.allowCredentials) };
  }
  function attestationJSON(cred) {
    const r = cred.response;
    return {
      id: cred.id, rawId: fromBuf(cred.rawId), type: cred.type,
      authenticatorAttachment: cred.authenticatorAttachment || undefined,
      response: {
        attestationObject: fromBuf(r.attestationObject),
        clientDataJSON: fromBuf(r.clientDataJSON),
        transports: typeof r.getTransports === "function" ? r.getTransports() : [],
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    };
  }
  function assertionJSON(cred) {
    const r = cred.response;
    return {
      id: cred.id, rawId: fromBuf(cred.rawId), type: cred.type,
      authenticatorAttachment: cred.authenticatorAttachment || undefined,
      response: {
        authenticatorData: fromBuf(r.authenticatorData),
        clientDataJSON: fromBuf(r.clientDataJSON),
        signature: fromBuf(r.signature),
        userHandle: r.userHandle ? fromBuf(r.userHandle) : undefined,
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    };
  }

  // ── 서버 ─────────────────────────────────────────────────────────────
  async function call(path, body) {
    const init = { cache: "no-store", credentials: "same-origin" };
    if (body !== undefined) {
      init.method = "POST";
      init.headers = { "Content-Type": "application/json" };
      init.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(path, init);
    } catch (_) {
      throw new Error("서버의 답을 받지 못했습니다. 폰의 연결과 mux serve·tailscale serve 가 떠 있는지 확인하세요.");
    }
    if (res.status === 403) throw new Error("접근이 거절됐습니다. tailscale serve 주소로 열었는지, 허용된 계정인지 확인하세요.");
    if (!res.ok) throw new Error(`서버 오류 ${res.status}`);
    return res.json();
  }

  // 브라우저의 패스키 창이 낸 오류를 사람 말로.
  function credentialError(e) {
    switch (e && e.name) {
      case "NotAllowedError": return "취소했거나 시간이 지났습니다. 다시 누르세요.";
      case "InvalidStateError": return "이 기기는 이미 등록되어 있습니다. «Face ID 로 열기» 를 누르세요.";
      case "SecurityError": return "이 주소에서는 패스키를 쓸 수 없습니다. tailscale serve 의 https 주소로 여세요.";
      default: return `패스키 창이 실패했습니다: ${(e && e.name) || "오류"} ${(e && e.message) || ""}`;
    }
  }

  // Safari 는 패스키 창을 «누른 그 순간» 에 열어야 할 수 있다 — 누른 뒤 서버에 묻는 await 가 끼면 사용자 동작을 잃고
  // 창이 안 뜰 수 있다. 그래서 도전값을 미리 받아 두고(prepare), 누르면 await 없이 navigator.credentials 를 부른다.
  // 서버의 의식은 3분이라 FRESH_MS 가 지난 것은 쓰지 않고, 화면이 다시 보일 때와 REFRESH_MS 마다 새로 받는다.
  const FRESH_MS = 2 * 60 * 1000;
  const REFRESH_MS = 90 * 1000;
  const ready = { login: null, register: null }; // {ceremony, publicKey, at, code}
  let working = false;
  const fresh = (r) => r && Date.now() - r.at < FRESH_MS;
  const typedCode = () => codeInput.value.replace(/\D/g, "");

  async function prepare(kind, body) {
    const begun = await call(`/auth/${kind}/begin`, body);
    if (begun.result !== "ok") return { result: begun.result };
    const publicKey = kind === "login" ? requestOptions(begun.options) : creationOptions(begun.options);
    return { result: "ok", r: { ceremony: begun.ceremony, publicKey, at: Date.now(), code: body.code } };
  }

  // refresh 는 도전값을 새로 받는다. quiet 면 잘 됐을 때 문구를 바꾸지 않는다(앞의 실패 문구를 지우지 않게).
  async function refresh(kind, quiet) {
    if (kind === "login") {
      if (unlockBox.hidden) return;
      try {
        const got = await prepare("login", {});
        ready.login = got.r || null;
        if (got.result !== "ok") say(RESULTS[got.result] || `실패: ${got.result}`, "stop");
      } catch (e) {
        say(e.message, "stop");
      }
      unlockBtn.disabled = working;
      return;
    }
    const code = typedCode();
    if (code.length !== 8) {
      ready.register = null;
      registerBtn.disabled = true;
      return;
    }
    try {
      const got = await prepare("register", { code });
      if (typedCode() !== code) return; // 그사이 코드를 고쳤다 — 새 입력이 다시 묻는다
      ready.register = got.r || null;
      registerBtn.disabled = working || !ready.register;
      if (got.result === "ok") {
        if (!quiet) say("코드를 확인했습니다. «이 기기 등록» 을 누르세요.", "ok");
      } else {
        say(RESULTS[got.result] || `실패: ${got.result}`, "stop");
      }
    } catch (e) {
      say(e.message, "stop");
    }
  }

  // press 는 누른 그 순간이다 — 미리 받은 도전값으로 await 전에 패스키 창을 연다.
  function press(kind) {
    if (working) return;
    const r = ready[kind];
    if (!fresh(r)) {
      say("준비하는 중입니다. 잠시 뒤 다시 누르세요.", "");
      refresh(kind, true);
      return;
    }
    ready[kind] = null; // 도전값은 한 번만 쓴다(서버도 그렇다)
    const pending = kind === "login"
      ? navigator.credentials.get({ publicKey: r.publicKey })
      : navigator.credentials.create({ publicKey: r.publicKey });
    finish(kind, r, pending);
  }

  async function finish(kind, r, pending) {
    working = true;
    unlockBtn.disabled = true;
    registerBtn.disabled = true;
    say(kind === "login" ? "Face ID 를 기다립니다…" : "이 기기를 등록합니다…", "");
    try {
      let cred;
      try {
        cred = await pending;
      } catch (e) {
        say(credentialError(e), "stop");
        return;
      }
      const credential = kind === "login" ? assertionJSON(cred) : attestationJSON(cred);
      const done = await call(`/auth/${kind}/finish`, { ceremony: r.ceremony, credential });
      if (done.result === "ok") {
        say("열었습니다.", "ok");
        location.replace("/");
        return;
      }
      say(RESULTS[done.result] || `실패: ${done.result}`, "stop");
    } catch (e) {
      say(e.message, "stop");
    } finally {
      working = false;
      if (!msg.dataset.tone || msg.dataset.tone === "stop") refresh(kind, true); // 다시 누를 수 있게
    }
  }

  function openEnroll() {
    enrollBox.hidden = false;
    showEnroll.hidden = true;
    codeInput.focus();
  }

  unlockBtn.addEventListener("click", () => press("login"));
  registerBtn.addEventListener("click", () => {
    if (typedCode().length !== 8) {
      say("데스크톱의 mux serve enroll 이 보여 준 여덟 자리 코드를 넣으세요.", "stop");
      codeInput.focus();
      return;
    }
    press("register");
  });
  let lastCode = "";
  codeInput.addEventListener("input", () => {
    const code = typedCode();
    if (code === lastCode) return;
    lastCode = code;
    refresh("register", false);
  });
  codeInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !registerBtn.disabled) press("register");
  });
  showEnroll.addEventListener("click", openEnroll);
  const refreshAll = () => {
    if (working) return;
    refresh("login", true);
    if (ready.register) refresh("register", true);
  };
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") refreshAll();
  });
  setInterval(refreshAll, REFRESH_MS);

  async function start() {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      say("이 브라우저는 패스키를 지원하지 않습니다. iPhone Safari 나 홈 화면의 Muxtap 으로 여세요.", "stop");
      return;
    }
    let st;
    try {
      st = await call("/auth/state");
    } catch (e) {
      say(e.message, "stop");
      return;
    }
    if (st.loggedIn) {
      location.replace("/");
      return;
    }
    if (st.registered < 0) say(RESULTS.broken, "stop");
    if (st.registered === 0) {
      say("처음이면 이 기기를 등록합니다.", "");
      openEnroll();
      return;
    }
    unlockBox.hidden = false;
    showEnroll.hidden = false;
    refresh("login", true);
  }
  unlockBtn.disabled = true;
  registerBtn.disabled = true;
  start();
})();
