# The E2B template definition of a release (§10.4): the published computer image of the same
# version, with no change. Fettle builds the same templates through the E2B API on first use
# (internal/providers/e2b): one template for each size of docs/computers.md, named
# fettle-<version>-<image hash>-<size>, with the CPU count and memory of that size.
#
# The templates have no start command. A template snapshot must not hold the token or the
# TLS key pair of a computer, so after create Fettle starts agentd as root through envd:
#
#   /usr/bin/tini -- /usr/local/bin/fettle-agentd
#
# with FETTLE_AGENTD_TOKEN, FETTLE_AGENTD_TLS_CERT, FETTLE_AGENTD_TLS_KEY,
# FETTLE_AGENTD_NETWORK and FETTLE_AGENTD_WS_ADDR=0.0.0.0:7422 (the WebSocket bridge that
# the E2B port URL reaches).
ARG VERSION=latest
FROM ghcr.io/alternayte/fettle-computer:${VERSION}
