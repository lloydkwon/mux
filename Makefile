BINARY  := mux
PREFIX  ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test e2e tmux-check herdr-check install clean

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/mux

test:
	go test ./...

# 격리된 진짜 tmux 서버 위에서 사용자가 보는 동작을 검사한다 — 창 크기, 클라이언트 이동,
# 패널 폭 저장값, 훅 응답 시간. 단위 테스트는 tmux 를 목으로 바꾸므로 이것들을 못 잡는다.
# 설치 전에 반드시 통과시킨다.
e2e:
	go test -tags e2e -count=1 -timeout 5m ./e2e/

# tmux 자신의 동작이 바뀌었는지 확인한다. 유닛 테스트는 tmux 를 목으로 대신하므로
# 여기서만 잡힌다 — apt/brew 로 tmux 를 올린 뒤에 돌린다.
tmux-check:
	./scripts/tmux-assumptions.sh

# herdr 자신의 응답 모양이 바뀌었는지 확인한다. 유닛 테스트는 캡처한 픽스처를
# 쓰므로 여기서만 잡힌다 — herdr update 뒤에 돌린다. herdr 안에서 돌려야 하고,
# 읽기만 하므로 워크스페이스를 건드리지 않는다.
herdr-check:
	./scripts/herdr-assumptions.sh

install: build
	install -d $(PREFIX)/bin
	install -m 755 $(BINARY) $(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)
