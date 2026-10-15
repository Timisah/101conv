# syntax=docker/dockerfile:1.7
FROM maven:3.9.12-eclipse-temurin-21 AS build
WORKDIR /workspace
COPY . .
RUN chmod +x ./mvnw
RUN --mount=type=secret,id=proxy_ca,required=false \
    set -eu; \
    maven_opts=""; \
    maven_settings=""; \
    if [ -s /run/secrets/proxy_ca ]; then \
      cp "$JAVA_HOME/lib/security/cacerts" /tmp/build-cacerts; \
      keytool -importcert -noprompt -trustcacerts -alias codex-session-proxy \
        -file /run/secrets/proxy_ca -keystore /tmp/build-cacerts -storepass changeit; \
      maven_opts="-Djavax.net.ssl.trustStore=/tmp/build-cacerts -Djavax.net.ssl.trustStorePassword=changeit"; \
    fi; \
    proxy_url="${HTTPS_PROXY:-${https_proxy:-${HTTP_PROXY:-${http_proxy:-}}}}"; \
    if [ -n "$proxy_url" ]; then \
      proxy_scheme="${proxy_url%%://*}"; \
      proxy_authority="${proxy_url#*://}"; \
      proxy_authority="${proxy_authority%%/*}"; \
      proxy_authority="${proxy_authority##*@}"; \
      proxy_host="${proxy_authority%%:*}"; \
      proxy_port="${proxy_authority##*:}"; \
      if [ "$proxy_host" != "$proxy_port" ]; then \
        printf '%s\n' '<settings><proxies><proxy><id>build-proxy</id><active>true</active><protocol>http</protocol><host>'"$proxy_host"'</host><port>'"$proxy_port"'</port></proxy></proxies></settings>' > /tmp/maven-settings.xml; \
        maven_settings="-s /tmp/maven-settings.xml"; \
      fi; \
    fi; \
    MAVEN_OPTS="$maven_opts" mvn $maven_settings -B package; \
    rm -f /tmp/build-cacerts /tmp/maven-settings.xml

FROM eclipse-temurin:21-jre
WORKDIR /var/lib/101conv
RUN groupadd --system --gid 10001 app \
    && useradd --system --uid 10001 --gid app --home-dir /var/lib/101conv app \
    && mkdir -p /opt/101conv /var/lib/101conv/tmp \
    && chown -R 10001:10001 /opt/101conv /var/lib/101conv
COPY --from=build --chown=10001:10001 /workspace/target/101conv-0.0.1-SNAPSHOT.jar /opt/101conv/app.jar
USER 10001:10001
ENV SERVER_PORT=5656 \
    APP_DATA_DIR=/var/lib/101conv \
    APP_USER_CONFIG_FILE=/var/lib/101conv/user-config.properties \
    SPRING_DATASOURCE_URL='jdbc:h2:file:/var/lib/101conv/convdb;MODE=MySQL'
EXPOSE 5656
ENTRYPOINT ["/bin/sh", "-c", "mkdir -p /var/lib/101conv/tmp && exec java -Djava.io.tmpdir=/var/lib/101conv/tmp -jar /opt/101conv/app.jar"]
