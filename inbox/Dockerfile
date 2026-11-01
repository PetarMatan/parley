# Inbox service image. Build context is the inbox/ directory:
#   docker build -f inbox/Dockerfile inbox
# The runtime image carries git because the local merge request provider
# (internal/mr, used until the GitLab provider lands) shells out to git diff.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/aic-inbox ./cmd/aic-inbox

FROM alpine:3.22
RUN apk add --no-cache git ca-certificates wget && adduser -D -u 10001 inbox \
 && mkdir -p /work && chown inbox:inbox /work
COPY --from=build /out/aic-inbox /usr/local/bin/aic-inbox
USER inbox
ENV AIC_WORK=/work AIC_INBOX_ADDR=0.0.0.0:7070
VOLUME ["/work"]
EXPOSE 7070
ENTRYPOINT ["/usr/local/bin/aic-inbox"]
