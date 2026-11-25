.PHONY: all ci clean
DIST_DIR ?= dist
SUFFIX ?= -android-arm64
MAIN_OUT := $(DIST_DIR)/webscreen$(SUFFIX)

all: ci
ci:
	@mkdir -p $(dir $(MAIN_OUT))
	GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -checklinkname=0" -o "$(MAIN_OUT)" .
clean:
	rm -rf $(DIST_DIR)
