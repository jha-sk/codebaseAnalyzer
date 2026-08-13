# The frontend is rebuilt here rather than trusting the committed dist/, so
# the image can never ship a bundle that disagrees with the source in it.
FROM node:22-alpine AS ui
WORKDIR /ui
COPY internal/dashboard/web/ui/package*.json ./
RUN npm ci
COPY internal/dashboard/web/ui/ ./
RUN npm run build          # writes ../dist relative to /ui, i.e. /dist

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed bundle with the one just built.
RUN rm -rf internal/dashboard/web/dist
COPY --from=ui /dist ./internal/dashboard/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboard ./cmd/dashboard

# Static binary, no shell, non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dashboard /dashboard
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/dashboard"]
