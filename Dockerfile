FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates \
    && addgroup -S trvl \
    && adduser -S -G trvl -h /home/trvl trvl
COPY trvl /usr/local/bin/trvl
USER trvl
WORKDIR /home/trvl
ENTRYPOINT ["trvl"]
