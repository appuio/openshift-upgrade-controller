IMG_TAG ?= latest

CURDIR ?= $(shell pwd)
BIN_FILENAME ?= $(CURDIR)/$(PROJECT_ROOT_DIR)/openshift-upgrade-controller

KUSTOMIZE ?= go tool sigs.k8s.io/kustomize/kustomize/v5

# Image URL to use all building/pushing image targets
GHCR_IMG ?= ghcr.io/appuio/openshift-upgrade-controller:$(IMG_TAG)
