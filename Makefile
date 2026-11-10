.PHONY: all dev release noterminal test clean fpk
V ?= 1.3.0
TERMINAL ?= 1
export TERMINAL

all: dev
fpk:
	TERMINAL=1 bash ./build.sh cross "$(V)"
	VERSION="$(V)" sh ./package-fpk.sh

dev release:
	bash ./build.sh $@ "$(V)"

noterminal:
	TERMINAL=0 bash ./build.sh dev "$(V)"

test:
	mkdir -p backend/embed
	test -e backend/embed/index.html || echo '<html></html>' > backend/embed/index.html
	cd backend && go test $(if $(filter 0,$(TERMINAL)),-tags noterminal) ./...

clean:
	rm -rf backend/embed
	rm -f backend/codex-console
