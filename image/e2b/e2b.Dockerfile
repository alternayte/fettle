# The E2B template of a release (§10.4): the published computer image of the same version.
# Fettle builds the template from this definition in the user's E2B account on first use.
ARG VERSION=latest
FROM ghcr.io/alternayte/fettle-computer:${VERSION}
