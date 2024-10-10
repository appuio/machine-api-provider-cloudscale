IMG_TAG ?= latest

CURDIR ?= $(shell pwd)
BIN_FILENAME ?= $(CURDIR)/$(PROJECT_ROOT_DIR)/machine-api-provider-cloudscale

# Image URL to use all building/pushing image targets
GHCR_IMG ?= ghcr.io/appuio/machine-api-provider-cloudscale:$(IMG_TAG)
