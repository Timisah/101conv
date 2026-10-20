# syntax=docker/dockerfile:1
FROM node:24-alpine@sha256:44fce696f38ba8c987c4db89954051000bbd05ffd8d8659ba15ed4a8602e937f AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=secret,id=proxy_ca NODE_EXTRA_CA_CERTS=/run/secrets/proxy_ca npm ci --ignore-scripts --strict-ssl=true
COPY . .
RUN npm run build

FROM nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
RUN chmod 0755 /etc/nginx /etc/nginx/conf.d \
    && chmod 0644 /etc/nginx/nginx.conf /etc/nginx/conf.d/default.conf \
    && chmod -R a+rX /usr/share/nginx/html
EXPOSE 5656
