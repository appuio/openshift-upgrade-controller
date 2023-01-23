IMG_TAG ?= latest

CURDIR ?= $(shell pwd)
BIN_FILENAME ?= $(CURDIR)/$(PROJECT_ROOT_DIR)/ocp-drain-monitor

KUSTOMIZE ?= go run sigs.k8s.io/kustomize/kustomize/v4

# Image URL to use all building/pushing image targets
GHCR_IMG ?= ghcr.io/appuio/ocp-drain-monitor:$(IMG_TAG)
