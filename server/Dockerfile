FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG APP_VERSION=0.1.0
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.buildVersion=${APP_VERSION}" -o /out/101conv ./cmd/101conv && chmod 0755 /out/101conv
RUN mkdir -p /out/var/lib/101conv/uploads /out/var/lib/101conv/results && chmod 0700 /out/var/lib/101conv/uploads /out/var/lib/101conv/results

FROM scratch
COPY --from=build /out/101conv /101conv
COPY --from=build --chown=65532:65532 /out/var/lib/101conv /var/lib/101conv
WORKDIR /var/lib/101conv
USER 65532:65532
ENV APP_VERSION=0.1.0 PORT=5656 DATA_PATH=/var/lib/101conv
EXPOSE 5656
ENTRYPOINT ["/101conv"]
