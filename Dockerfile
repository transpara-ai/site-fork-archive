# ── CSS build stage ─────────────────────────────────────────────────
# Node is used only here to run the Tailwind v4 CLI. Output CSS is
# copied into the go builder stage; node_modules never leaves.
FROM node:20-alpine AS css-builder

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

# Content sources that Tailwind scans for utility classes + input.css.
COPY static ./static
COPY views ./views
COPY graph ./graph
COPY cmd ./cmd

RUN npx @tailwindcss/cli \
    -i ./static/css/input.css \
    -o ./static/css/site.css \
    --minify

# ── Go build stage ──────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN go install github.com/a-h/templ/cmd/templ@latest

WORKDIR /app
COPY go.mod go.sum ./

# Build uses vendored deps (populated by `go mod vendor` on the host before
# building this image). The vendor dir is required because go.mod has a `replace`
# directive pointing at the sibling eventgraph repo, which is outside the
# Docker build context.
COPY . .
COPY --from=css-builder /app/static/css/site.css ./static/css/site.css
RUN templ generate
RUN CGO_ENABLED=0 go build -mod=vendor -o /site ./cmd/site/

# ── Final stage ─────────────────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ca-certificates nodejs npm
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /site /usr/local/bin/site
COPY --from=builder /app/static /static

EXPOSE 8080
CMD ["/usr/local/bin/site"]
