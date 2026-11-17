// mux serve 의 녹음(docs/design/serve.md «받아쓰기 — 서버 Whisper»). 마이크를 MediaRecorder 로 녹음해 Blob 으로
// 내고, 듣는 동안 소리 크기를 알린다. 서버에 올리고 글을 받는 것은 app.js 다.
"use strict";
window.MuxRecorder = (() => {
  // iPhone Safari 는 18.4 부터 webm(Opus), 그 전에는 mp4(AAC)로만 녹음한다. 서버는 이 둘만 받는다.
  const TYPES = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4"];
  const STOP_GRACE_MS = 2000; // «그만» 뒤 끝 신호를 기다리는 시간
  const pickType = () => {
    if (!window.MediaRecorder || typeof MediaRecorder.isTypeSupported !== "function") return "";
    return TYPES.find((t) => MediaRecorder.isTypeSupported(t)) || "";
  };
  const supported = () => !!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia) && !!pickType();
  // 서버에 보낼 Content-Type — codecs 를 뗀 audio/webm·audio/mp4.
  const baseType = (t) => (t || "").split(";")[0].trim();

  // levelMeter 는 소리 크기(0..1)를 100ms 마다 onLevel 로 알린다. AudioContext 는 누른 순간(사용자 동작 안)에 만들어야
  // iOS 가 멈춘 채로 두지 않는다 — 그래서 마이크를 묻기 전에 만들고, 마이크가 열리면 잇는다.
  function levelMeter() {
    const AC = window.AudioContext || window.webkitAudioContext;
    let ctx = null;
    try { ctx = AC ? new AC() : null; } catch (_) { ctx = null; }
    let timer = 0;
    return {
      attach(stream, onLevel) {
        if (!ctx) return;
        try {
          ctx.resume().catch(() => {});
          const an = ctx.createAnalyser();
          an.fftSize = 512;
          ctx.createMediaStreamSource(stream).connect(an);
          const buf = new Uint8Array(an.fftSize);
          timer = setInterval(() => {
            an.getByteTimeDomainData(buf);
            let sum = 0;
            for (const v of buf) sum += ((v - 128) / 128) ** 2;
            onLevel(Math.min(1, Math.sqrt(sum / buf.length) * 4));
          }, 100);
        } catch (_) { /* 크기 막대 없이 녹음만 한다 */ }
      },
      close() {
        clearInterval(timer);
        if (ctx) ctx.close().catch(() => {});
        ctx = null;
      },
    };
  }

  // start 는 마이크를 열고 녹음을 시작한다. 마이크를 열지 못하면 그 오류(name: NotAllowedError 등)로 거절된다.
  // 녹음은 maxMs 에 onAuto() 를 부른다(멈추는 것은 부른 쪽). 돌려주는 것:
  //   stop()   → Promise<{blob, type, ms}> — 마이크를 놓고 녹음을 낸다
  //   cancel() → 마이크를 놓고 녹음을 버린다
  async function start({ maxMs, onLevel, onAuto }) {
    const type = pickType();
    if (!type) throw Object.assign(new Error("이 브라우저는 녹음할 수 없습니다"), { name: "NotSupportedError" });
    const meter = levelMeter();
    let stream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true } });
    } catch (e) {
      meter.close();
      throw e;
    }
    const chunks = [];
    let rec;
    try {
      // 말소리에는 32kbps 면 된다 — Chrome 의 기본값으로는 2분이 1.9MB 였고, 32kbps Opus 는 원본과 같은 글이 나왔다.
      // 폰 망에서 올리는 시간이 줄어든다.
      rec = new MediaRecorder(stream, { mimeType: type, audioBitsPerSecond: 32000 });
      rec.ondataavailable = (e) => { if (e.data && e.data.size) chunks.push(e.data); };
      // 조각으로 나눠 받지 않는다(timeslice 없음) — 멈출 때 한 덩어리로 받는다. Safari 가 조각마다 내는 mp4 를 이어
      // 붙인 파일이 온전한지는 재지 않았고, 한 덩어리면 그 걱정이 없다.
      rec.start();
    } catch (e) {
      // 만들기나 시작이 실패해도 마이크를 놓는다 — 놓지 않으면 iPhone 의 마이크 표시가 페이지가 살아 있는 동안 켜져 있다.
      stream.getTracks().forEach((t) => t.stop());
      meter.close();
      throw e;
    }
    let autoTimer = 0;
    let finished = null;
    // 끝나는 즉시 마이크 트랙을 멈춘다 — iPhone 의 마이크 표시가 남지 않게.
    const release = () => {
      clearTimeout(autoTimer);
      stream.getTracks().forEach((t) => t.stop());
      meter.close();
    };
    const started = Date.now();
    meter.attach(stream, onLevel || (() => {}));
    autoTimer = setTimeout(() => { if (onAuto) onAuto(); }, maxMs);

    // stop 은 녹음을 낸다. 끝 신호(stop 사건)가 STOP_GRACE_MS 안에 오지 않으면 그때까지 받은 것으로 마친다 — 브라우저
    // 받아쓰기에서 iOS 가 끝 신호를 안 보낸 일이 있었다. MediaRecorder 도 그런지는 재지 않았지만, 기다림이 끝나지 않으면
    // 마이크가 켜진 채 버튼이 «받아쓰는 중» 에 묶인다. 받은 것이 없으면 빈 녹음이다(부른 쪽이 «녹음이 비었습니다»).
    function stop() {
      if (finished) return finished;
      finished = new Promise((resolve) => {
        let settled = false;
        const done = () => {
          if (settled) return;
          settled = true;
          clearTimeout(grace);
          release();
          const t = baseType(rec.mimeType || type);
          resolve({ blob: new Blob(chunks, { type: t }), type: t, ms: Date.now() - started });
        };
        const grace = setTimeout(done, STOP_GRACE_MS);
        if (rec.state === "inactive") {
          done();
          return;
        }
        rec.addEventListener("stop", done, { once: true });
        try { rec.stop(); } catch (_) { done(); }
      });
      return finished;
    }
    function cancel() {
      if (finished) return;
      finished = Promise.resolve(null);
      try { rec.stop(); } catch (_) { /* 이미 멈췄다 */ }
      release();
    }
    return { stop, cancel, started };
  }

  return Object.freeze({ supported, start });
})();
