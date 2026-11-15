# The fettle server image (§17): the UI and the binary, with no compiler and no shell
# tools beyond CA certificates. The computers run in their own image (image/Dockerfile).

FROM oven/bun:1.4.3 AS ui
WORKDIR /src/ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui ./
RUN bun run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/fettle ./cmd/fettle \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/fettle-agentd ./cmd/fettle-agentd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/fettle /out/fettle-agentd /usr/local/bin/
ENV FETTLE_DATA=/data FETTLE_ADDR=:7420 FETTLE_NO_KEYCHAIN=1
VOLUME /data
EXPOSE 7420
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/fettle"]
CMD ["serve"]
