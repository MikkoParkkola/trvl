FROM alpine:3.23
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates \
    && addgroup -S trvl \
    && adduser -S -G trvl -h /home/trvl trvl
COPY trvl /usr/local/bin/trvl
USER trvl
WORKDIR /home/trvl
ENTRYPOINT ["trvl"]
