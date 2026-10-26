FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates \
    && addgroup -S trvl \
    && adduser -S -G trvl -h /home/trvl trvl
COPY trvl /usr/local/bin/trvl
USER trvl
WORKDIR /home/trvl
ENTRYPOINT ["trvl"]
