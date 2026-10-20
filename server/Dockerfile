# syntax=docker/dockerfile:1
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG APP_VERSION=0.1.0
RUN --mount=type=secret,id=proxy_ca,required=false \
    if [ -f /run/secrets/proxy_ca ]; then \
      cat /etc/ssl/certs/ca-certificates.crt /run/secrets/proxy_ca >/tmp/101conv-ca.pem; \
      export SSL_CERT_FILE=/tmp/101conv-ca.pem; \
    fi && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.buildVersion=${APP_VERSION}" -o /out/101conv ./cmd/101conv && chmod 0755 /out/101conv
RUN mkdir -p /out/var/lib/101conv/uploads /out/var/lib/101conv/results && chmod 0700 /out/var/lib/101conv/uploads /out/var/lib/101conv/results

FROM scratch
COPY --from=build /out/101conv /101conv
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/var/lib/101conv /var/lib/101conv
WORKDIR /var/lib/101conv
USER 65532:65532
ENV APP_VERSION=0.1.0 PORT=5656 DATA_PATH=/var/lib/101conv
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
EXPOSE 5656
ENTRYPOINT ["/101conv"]
