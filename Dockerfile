FROM arm32v7/golang:1.19-alpine as builder
WORKDIR /home/ftp-client
COPY . .
RUN go build -o main main.go

FROM arm32v7/alpine
WORKDIR /home/ftp-client
COPY --from=builder /home/ftp-client/main .
CMD [ "/home/ftp-client/main" ]
