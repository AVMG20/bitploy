package deploy

import (
	"reflect"
	"strings"
	"testing"
)

// composeCase is one compose file and what must come out of it.
type composeCase struct {
	name  string
	file  string
	path  string            // compose file path in the source; "" is compose.yaml
	env   map[string]string // shared variables
	files map[string]string // the source; nil: a pasted file (no source)
	// errCode is the code of the first error; "" means the file is valid.
	errCode, errService, errKey, errText string
	warn                                 []string // warning codes that must be there
	check                                func(t *testing.T, p ComposeProject)
}

func runComposeCase(t *testing.T, c composeCase) ComposeProject {
	t.Helper()
	in := ComposeInput{Content: []byte(c.file), Path: c.path, Env: c.env}
	if c.files != nil {
		in.Read = func(rel string) []byte {
			if v, ok := c.files[rel]; ok {
				return []byte(v)
			}
			return nil
		}
	}
	p := ParseCompose(in)
	if c.errCode == "" {
		if len(p.Errors) > 0 {
			t.Fatalf("unexpected errors: %+v", p.Errors)
		}
	} else {
		if len(p.Errors) == 0 {
			t.Fatalf("want error %s, got none (services %+v)", c.errCode, p.Services)
		}
		e := p.Errors[0]
		if e.Code != c.errCode || (c.errService != "" && e.Service != c.errService) || (c.errKey != "" && e.Key != c.errKey) {
			t.Fatalf("want %s on %s/%s, got %+v", c.errCode, c.errService, c.errKey, p.Errors)
		}
		if c.errText != "" && !strings.Contains(e.Message+" "+e.Hint, c.errText) {
			t.Fatalf("error %q (hint %q) does not mention %q", e.Message, e.Hint, c.errText)
		}
		if e.Hint == "" {
			t.Fatalf("error without a hint: %+v", e)
		}
	}
	for _, w := range c.warn {
		found := false
		for _, got := range p.Warnings {
			if got.Code == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("want warning %s, got %+v", w, p.Warnings)
		}
	}
	if c.check != nil {
		c.check(t, p)
	}
	return p
}

func svcNamed(t *testing.T, p ComposeProject, name string) ComposeService {
	t.Helper()
	for _, s := range p.Services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no service %s in %+v", name, p.Services)
	return ComposeService{}
}

func TestParseComposeRealWorld(t *testing.T) {
	cases := []composeCase{
		{
			name: "app and postgres",
			file: `
version: "3.8"
services:
  web:
    build: .
    ports:
      - "8080:3000"
    environment:
      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app
      NODE_ENV: production
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres:16-alpine
    environment:
      - POSTGRES_PASSWORD=${DB_PASSWORD}
      - POSTGRES_USER=app
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 1m
volumes:
  pgdata:
`,
			env:  map[string]string{"DB_PASSWORD": "s3cret"},
			warn: []string{CodeComposePortsIgnored},
			check: func(t *testing.T, p ComposeProject) {
				if len(p.Services) != 2 || p.Services[0].Name != "web" || p.Services[1].Name != "db" {
					t.Fatalf("services in file order: %+v", p.Services)
				}
				web, db := p.Services[0], p.Services[1]
				if !web.Public || web.Port != 3000 || web.Kind != "build" || web.Context != "." || web.Dockerfile != "Dockerfile" || web.HealthPath != "/" {
					t.Fatalf("web: %+v", web)
				}
				if web.MemoryMB != ComposePublicBuildMemoryMB || web.CPUMillis != ComposeDefaultCPUMillis {
					t.Fatalf("web defaults: %d MB %d", web.MemoryMB, web.CPUMillis)
				}
				if db.Public || db.Kind != "image" || db.Image != "postgres:16-alpine" || db.MemoryMB != ComposeDefaultMemoryMB {
					t.Fatalf("db: %+v", db)
				}
				// Shared variables stay markers: the secret is not in the result.
				if got := web.Environment["DATABASE_URL"]; got != "postgres://app:"+EnvRef("DB_PASSWORD")+"@db:5432/app" {
					t.Fatalf("DATABASE_URL = %q", got)
				}
				if db.Environment["POSTGRES_PASSWORD"] != EnvRef("DB_PASSWORD") || db.Environment["POSTGRES_USER"] != "app" {
					t.Fatalf("db env: %v", db.Environment)
				}
				if !reflect.DeepEqual(web.DependsOn, []ComposeDependency{{Service: "db", Condition: "service_healthy"}}) {
					t.Fatalf("depends_on: %+v", web.DependsOn)
				}
				hc := db.Healthcheck
				if hc == nil || hc.Test[0] != "CMD-SHELL" || hc.IntervalS != 5 || hc.TimeoutS != 3 || hc.Retries != 10 || hc.StartPeriodS != 60 {
					t.Fatalf("healthcheck: %+v", hc)
				}
				if !reflect.DeepEqual(p.Volumes, []ComposeVolume{{Name: "pgdata", SizeMB: ComposeDefaultVolumeMB, Service: "db", MountPath: "/var/lib/postgresql/data"}}) {
					t.Fatalf("volumes: %+v", p.Volumes)
				}
			},
		},
		{
			name: "app, redis and a worker",
			file: `
services:
  web:
    build: .
    command: node server.js
    expose: ["3000"]
    environment:
      REDIS_URL: redis://cache:6379
    depends_on: [cache]
  worker:
    build: .
    command: ["node", "worker.js"]
    depends_on:
      - cache
  cache:
    image: redis:7-alpine
    command: redis-server --requirepass ${REDIS_PASSWORD} --appendonly yes
`,
			env: map[string]string{"REDIS_PASSWORD": "pw"},
			check: func(t *testing.T, p ComposeProject) {
				web, worker, cache := svcNamed(t, p, "web"), svcNamed(t, p, "worker"), svcNamed(t, p, "cache")
				if !web.Public || web.Port != 3000 || worker.Public || cache.Public {
					t.Fatalf("public: web %v:%d worker %v cache %v", web.Public, web.Port, worker.Public, cache.Public)
				}
				if !reflect.DeepEqual(web.Command, []string{"node", "server.js"}) || !reflect.DeepEqual(worker.Command, []string{"node", "worker.js"}) {
					t.Fatalf("commands: %q %q", web.Command, worker.Command)
				}
				if !reflect.DeepEqual(cache.Command, []string{"redis-server", "--requirepass", EnvRef("REDIS_PASSWORD"), "--appendonly", "yes"}) {
					t.Fatalf("cache command: %q", cache.Command)
				}
				if worker.MemoryMB != ComposeDefaultMemoryMB {
					t.Fatalf("worker memory %d", worker.MemoryMB)
				}
				if !reflect.DeepEqual(worker.DependsOn, []ComposeDependency{{Service: "cache", Condition: "service_started"}}) {
					t.Fatalf("short depends_on: %+v", worker.DependsOn)
				}
			},
		},
		{
			name: "next.js and postgres with a published database port",
			file: `
services:
  app:
    build:
      context: ./web
      dockerfile: Dockerfile.prod
      target: runner
      args:
        NEXT_PUBLIC_API: https://example.com
    ports: ["3000:3000"]
    env_file: ./web/.env.production
    deploy:
      resources:
        limits:
          memory: 1g
          cpus: "0.5"
  postgres:
    image: postgres:${PG_VERSION:-16}
    ports:
      - 5432:5432
    volumes:
      - type: volume
        source: db-data
        target: /var/lib/postgresql/data
volumes:
  db-data:
    x-bitploy:
      size_mb: 4096
`,
			files: map[string]string{"web/.env.production": "NEXT_TELEMETRY_DISABLED=1\nAPI_KEY=${API_KEY}\n", "web/Dockerfile.prod": "FROM node:24-alpine\nEXPOSE 3000\n"},
			env:   map[string]string{"API_KEY": "k"},
			check: func(t *testing.T, p ComposeProject) {
				app, pg := svcNamed(t, p, "app"), svcNamed(t, p, "postgres")
				if !app.Public || pg.Public {
					t.Fatalf("the built service wins over an image that publishes a port: app %v postgres %v", app.Public, pg.Public)
				}
				if app.Context != "web" || app.Dockerfile != "web/Dockerfile.prod" || app.Target != "runner" || app.Args["NEXT_PUBLIC_API"] != "https://example.com" {
					t.Fatalf("build: %+v", app)
				}
				if app.MemoryMB != 1024 || app.CPUMillis != 500 {
					t.Fatalf("limits: %d MB %d", app.MemoryMB, app.CPUMillis)
				}
				if app.Environment["NEXT_TELEMETRY_DISABLED"] != "1" || app.Environment["API_KEY"] != EnvRef("API_KEY") {
					t.Fatalf("env_file: %v", app.Environment)
				}
				if pg.Image != "postgres:16" {
					t.Fatalf("${VAR:-default} in image: %q", pg.Image)
				}
				if len(p.Volumes) != 1 || p.Volumes[0].SizeMB != 4096 || p.Volumes[0].Name != "db-data" {
					t.Fatalf("volumes: %+v", p.Volumes)
				}
			},
		},
		{
			name: "ports 8080:3000 serves 3000",
			file: "services:\n  web:\n    image: nginx:alpine\n    ports: [\"8080:3000\"]\n",
			check: func(t *testing.T, p ComposeProject) {
				if s := p.Services[0]; !s.Public || s.Port != 3000 {
					t.Fatalf("%+v", s)
				}
			},
		},
		{
			name: "defaults with ${VAR:-default}",
			file: `
services:
  web:
    image: ghcr.io/acme/web:${TAG:-latest}
    ports: ["${APP_PORT:-8000}"]
    environment:
      LEVEL: ${LOG_LEVEL:-info}
      EMPTY: ${EMPTY_VAR:-fallback}
      SET: ${SET_VAR:-unused}
      BARE:
`,
			env: map[string]string{"EMPTY_VAR": "", "SET_VAR": "real", "BARE": "x"},
			check: func(t *testing.T, p ComposeProject) {
				s := p.Services[0]
				if s.Image != "ghcr.io/acme/web:latest" || s.Port != 8000 {
					t.Fatalf("%+v", s)
				}
				if s.Environment["LEVEL"] != "info" || s.Environment["EMPTY"] != "fallback" || s.Environment["SET"] != EnvRef("SET_VAR") {
					t.Fatalf("env: %v", s.Environment)
				}
			},
		},
		{
			name: "unset variable without default warns",
			file: "services:\n  web:\n    image: nginx\n    environment:\n      SECRET: ${SECRET}\n",
			warn: []string{CodeComposeVariableUnset},
		},
		{
			name:    "required variable",
			file:    "services:\n  web:\n    image: nginx\n    environment:\n      SECRET: ${SECRET:?set SECRET first}\n",
			errCode: CodeComposeInvalid, errText: "set_env",
		},
		{
			name: "x-bitploy marks the public service",
			file: `
services:
  api:
    build: ./api
    ports: ["4000"]
  site:
    build: ./site
    ports: ["3000"]
    x-bitploy:
      public: true
      port: 3001
      health_path: /healthz
`,
			check: func(t *testing.T, p ComposeProject) {
				site, api := svcNamed(t, p, "site"), svcNamed(t, p, "api")
				if !site.Public || site.Port != 3001 || site.HealthPath != "/healthz" || api.Public {
					t.Fatalf("site %+v api %+v", site, api)
				}
			},
		},
		{
			name: "only workers",
			file: "services:\n  bot:\n    build: .\n  cache:\n    image: redis:7\n",
			check: func(t *testing.T, p ComposeProject) {
				if p.Public() != nil {
					t.Fatalf("no service should be public: %+v", p.Public())
				}
			},
		},
		{
			name:  "EXPOSE in the Dockerfile picks the public service",
			file:  "services:\n  web:\n    build: .\n  db:\n    image: postgres:16\n",
			files: map[string]string{"Dockerfile": "FROM node:24-alpine\nEXPOSE 8080\nCMD [\"node\",\"s.js\"]\n"},
			check: func(t *testing.T, p ComposeProject) {
				if pub := p.Public(); pub == nil || pub.Name != "web" || pub.Port != 8080 {
					t.Fatalf("public: %+v", pub)
				}
			},
		},
		{
			name:  "compose file in a folder",
			path:  "deploy/compose.yaml",
			file:  "services:\n  web:\n    build:\n      context: ..\n      dockerfile: docker/Dockerfile\n    ports: [\"3000\"]\n    env_file: [../.env.example]\n",
			files: map[string]string{".env.example": "A=1\n"},
			check: func(t *testing.T, p ComposeProject) {
				s := p.Services[0]
				if s.Context != "." || s.Dockerfile != "docker/Dockerfile" || s.Environment["A"] != "1" {
					t.Fatalf("%+v", s)
				}
			},
		},
		{
			name:  "optional env_file that is missing",
			file:  "services:\n  web:\n    image: nginx\n    env_file:\n      - path: ./.env\n        required: false\n",
			files: map[string]string{},
		},
		{
			name:    "env_file that is missing",
			file:    "services:\n  web:\n    image: nginx\n    env_file: .env\n",
			files:   map[string]string{},
			errCode: CodeComposeEnvFileMissing, errService: "web", errKey: "env_file[0]",
		},
		{
			name: "env_file of a pasted file is not checked",
			file: "services:\n  web:\n    image: nginx\n    env_file: .env\n",
			warn: []string{CodeComposeEnvFileMissing},
		},
		{
			name:    "env_file outside the project",
			file:    "services:\n  web:\n    image: nginx\n    env_file: ../../etc/passwd\n",
			files:   map[string]string{},
			errCode: CodeComposeForbidden, errService: "web", errKey: "env_file[0]",
		},
		{
			name: "healthcheck disabled and restart ignored",
			file: "services:\n  web:\n    image: nginx\n    restart: unless-stopped\n    healthcheck:\n      disable: true\n",
			check: func(t *testing.T, p ComposeProject) {
				if hc := p.Services[0].Healthcheck; hc == nil || hc.Test[0] != "NONE" {
					t.Fatalf("%+v", hc)
				}
				if len(p.Warnings) != 0 {
					t.Fatalf("restart is ignored silently: %+v", p.Warnings)
				}
			},
		},
		{
			name: "string healthcheck, user and working_dir",
			file: "services:\n  web:\n    image: nginx\n    user: \"1000:1000\"\n    working_dir: /srv\n    healthcheck:\n      test: curl -f http://localhost/ || exit 1\n",
			check: func(t *testing.T, p ComposeProject) {
				s := p.Services[0]
				if s.User != "1000:1000" || s.WorkingDir != "/srv" || !reflect.DeepEqual(s.Healthcheck.Test, []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"}) {
					t.Fatalf("%+v %+v", s, s.Healthcheck)
				}
			},
		},
		{
			name: "anonymous and tmpfs volumes are ignored",
			file: "services:\n  web:\n    image: nginx\n    volumes:\n      - /var/cache/nginx\n      - type: tmpfs\n        target: /tmp\n",
			warn: []string{CodeComposeKeyIgnored},
			check: func(t *testing.T, p ComposeProject) {
				if len(p.Services[0].Mounts) != 0 || len(p.Volumes) != 0 {
					t.Fatalf("%+v %+v", p.Services[0].Mounts, p.Volumes)
				}
			},
		},
		{
			name: "unknown keys warn",
			file: "services:\n  web:\n    image: nginx\n    container_name: my-web\n    logging:\n      driver: json-file\n    links: [db]\n  db:\n    image: postgres\nx-common: &c\n  a: 1\n",
			warn: []string{CodeComposeKeyIgnored},
			check: func(t *testing.T, p ComposeProject) {
				keys := map[string]bool{}
				for _, w := range p.Warnings {
					keys[w.Service+"/"+w.Key] = true
				}
				for _, k := range []string{"web/container_name", "web/logging", "web/links"} {
					if !keys[k] {
						t.Fatalf("no warning for %s: %+v", k, p.Warnings)
					}
				}
			},
		},
		{
			name: "profiles leave a service out",
			file: "services:\n  web:\n    image: nginx\n  adminer:\n    image: adminer\n    profiles: [debug]\n",
			warn: []string{CodeComposeServiceSkipped},
			check: func(t *testing.T, p ComposeProject) {
				if len(p.Services) != 1 || p.Services[0].Name != "web" {
					t.Fatalf("%+v", p.Services)
				}
			},
		},
		{
			name: "yaml anchors and merge keys",
			file: `
x-env: &env
  environment:
    NODE_ENV: production
services:
  web:
    <<: *env
    build: .
    ports: ["3000"]
  worker:
    <<: *env
    build: .
    command: node worker.js
`,
			check: func(t *testing.T, p ComposeProject) {
				if svcNamed(t, p, "worker").Environment["NODE_ENV"] != "production" {
					t.Fatalf("%+v", p.Services)
				}
			},
		},

		// ---- rejected ----
		{
			name:    "bind mount",
			file:    "services:\n  web:\n    image: nginx\n    volumes:\n      - ./html:/usr/share/nginx/html\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "volumes[0]", errText: "named volume",
		},
		{
			name:    "bind mount, long syntax",
			file:    "services:\n  web:\n    image: nginx\n    volumes:\n      - type: bind\n        source: /etc\n        target: /host-etc\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "volumes[0]",
		},
		{
			name:    "docker socket",
			file:    "services:\n  traefik:\n    image: traefik:v3\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock:ro\n",
			errCode: CodeComposeForbidden, errService: "traefik", errKey: "volumes[0]", errText: "Docker socket",
		},
		{
			name:    "privileged",
			file:    "services:\n  web:\n    image: nginx\n    privileged: true\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "privileged",
		},
		{
			name: "privileged false is fine",
			file: "services:\n  web:\n    image: nginx\n    privileged: false\n",
		},
		{
			name:    "cap_add",
			file:    "services:\n  vpn:\n    image: wireguard\n    cap_add: [NET_ADMIN]\n",
			errCode: CodeComposeForbidden, errService: "vpn", errKey: "cap_add",
		},
		{
			name:    "network_mode host",
			file:    "services:\n  web:\n    image: nginx\n    network_mode: host\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "network_mode",
		},
		{
			name:    "custom networks",
			file:    "services:\n  web:\n    image: nginx\n    networks: [front]\nnetworks:\n  front:\n",
			errCode: CodeComposeForbidden, errKey: "networks",
		},
		{
			name:    "secrets",
			file:    "services:\n  web:\n    image: nginx\n    secrets: [db_pw]\nsecrets:\n  db_pw:\n    file: ./pw.txt\n",
			errCode: CodeComposeForbidden, errKey: "secrets", errText: "set_env",
		},
		{
			name:    "extends",
			file:    "services:\n  base:\n    image: nginx\n  web:\n    extends:\n      file: ../other.yml\n      service: base\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "extends",
		},
		{
			name:    "include",
			file:    "include:\n  - ../other/compose.yaml\nservices:\n  web:\n    image: nginx\n",
			errCode: CodeComposeForbidden, errKey: "include",
		},
		{
			name:    "external volume",
			file:    "services:\n  db:\n    image: postgres\n    volumes: [\"data:/var/lib/postgresql/data\"]\nvolumes:\n  data:\n    external: true\n",
			errCode: CodeComposeForbidden, errKey: "volumes.data.external",
		},
		{
			name:    "build context outside the project",
			file:    "services:\n  web:\n    build: ../other\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "build.context",
		},
		{
			name:    "remote build context",
			file:    "services:\n  web:\n    build: https://github.com/acme/web.git\n",
			errCode: CodeComposeForbidden, errService: "web", errKey: "build.context",
		},
		{
			name:    "bitploy image",
			file:    "services:\n  web:\n    image: bitploy/other-app:3\n",
			errCode: CodeBaseForbidden, errService: "web", errKey: "image",
		},
		{
			name: "more than six services",
			file: "services:\n" + strings.Repeat("", 0) +
				"  a:\n    image: nginx\n  b:\n    image: nginx\n  c:\n    image: nginx\n  d:\n    image: nginx\n  e:\n    image: nginx\n  f:\n    image: nginx\n  g:\n    image: nginx\n",
			errCode: CodeComposeTooManyServices,
		},
		{
			name:    "several build services with ports",
			file:    "services:\n  api:\n    build: ./api\n    ports: [\"4000:4000\"]\n  web:\n    build: ./web\n    ports: [\"3000:3000\"]\n",
			errCode: CodeComposePublicAmbiguous, errText: "x-bitploy",
		},
		{
			name:    "two services marked public",
			file:    "services:\n  a:\n    image: nginx\n    x-bitploy: {public: true}\n  b:\n    image: nginx\n    x-bitploy: {public: true}\n",
			errCode: CodeComposePublicAmbiguous,
		},
		{
			name:    "a volume shared by two services",
			file:    "services:\n  web:\n    build: .\n    volumes: [\"data:/data\"]\n  worker:\n    build: .\n    volumes: [\"data:/data\"]\nvolumes:\n  data:\n",
			errCode: CodeComposeVolumeShared, errService: "worker",
		},
		{
			name:    "a volume that is not declared",
			file:    "services:\n  db:\n    image: postgres\n    volumes: [\"pg:/var/lib/postgresql/data\"]\n",
			errCode: CodeComposeInvalid, errService: "db",
		},
		{
			name:    "depends_on an unknown service",
			file:    "services:\n  web:\n    image: nginx\n    depends_on: [db]\n",
			errCode: CodeComposeInvalid, errService: "web", errKey: "depends_on",
		},
		{
			name:    "depends_on service_completed_successfully",
			file:    "services:\n  migrate:\n    build: .\n    command: npm run migrate\n  web:\n    build: .\n    depends_on:\n      migrate:\n        condition: service_completed_successfully\n",
			errCode: CodeComposeUnsupported, errService: "web", errKey: "depends_on.migrate",
		},
		{
			name:    "depends_on in a circle",
			file:    "services:\n  a:\n    image: nginx\n    depends_on: [b]\n  b:\n    image: nginx\n    depends_on: [a]\n",
			errCode: CodeComposeInvalid, errKey: "depends_on", errText: "circle",
		},
		{
			name:    "service name with capitals",
			file:    "services:\n  Web:\n    image: nginx\n",
			errCode: CodeComposeServiceNameInvalid, errService: "Web",
		},
		{
			name:    "service name with an underscore",
			file:    "services:\n  celery_worker:\n    image: nginx\n",
			errCode: CodeComposeServiceNameInvalid, errService: "celery_worker", errText: "celery_worker",
		},
		{
			name:    "service name like another app's container",
			file:    "services:\n  bp-victim-3:\n    image: nginx\n",
			errCode: CodeComposeServiceNameInvalid, errService: "bp-victim-3", errText: "reserved",
		},
		{
			name:    "service name like Traefik's container",
			file:    "services:\n  bitploy-traefik:\n    image: nginx\n",
			errCode: CodeComposeServiceNameInvalid, errService: "bitploy-traefik", errText: "reserved",
		},
		{
			name:    "image name with spaces",
			file:    "services:\n  web:\n    image: \"nginx RUN rm\"\n",
			errCode: CodeComposeInvalid, errService: "web", errKey: "image",
		},
		{
			name:    "no image and no build",
			file:    "services:\n  web:\n    command: echo hi\n",
			errCode: CodeComposeInvalid, errService: "web",
		},
		{
			name:    "not yaml",
			file:    "services:\n  web:\n   image: nginx\n  - broken",
			errCode: CodeComposeInvalid,
		},
		{
			name:    "no services",
			file:    "volumes:\n  data:\n",
			errCode: CodeComposeNoServices,
		},
		{
			name:    "replicas",
			file:    "services:\n  web:\n    image: nginx\n    deploy:\n      replicas: 3\n",
			errCode: CodeComposeUnsupported, errService: "web", errKey: "deploy.replicas",
		},
		{
			name:    "reserved port",
			file:    "services:\n  web:\n    image: nginx\n    ports: [\"8081\"]\n",
			errCode: CodePortReserved, errService: "web",
		},
		{
			name:    "schema error",
			file:    "services:\n  web:\n    image: nginx\n    ports: {a: b}\n",
			errCode: CodeComposeInvalid,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runComposeCase(t, c) })
	}
}

func TestParseComposeNeverLeaksSharedValues(t *testing.T) {
	file := `
services:
  web:
    image: nginx
    command: ["sh", "-c", "echo ${TOKEN}"]
    entrypoint: /bin/start --token=${TOKEN}
    environment:
      A: ${TOKEN}
      B: prefix-${TOKEN}-suffix
    healthcheck:
      test: ["CMD", "check", "${TOKEN}"]
`
	p := runComposeCase(t, composeCase{file: file, env: map[string]string{"TOKEN": "top-secret-value"}})
	s := p.Services[0]
	all := strings.Join(s.Command, " ") + strings.Join(s.Entrypoint, " ") + strings.Join(s.Healthcheck.Test, " ")
	for _, v := range s.Environment {
		all += v
	}
	if strings.Contains(all, "top-secret-value") {
		t.Fatalf("a shared value was copied into the result: %q", all)
	}
	look := func(n string) (string, bool) { return map[string]string{"TOKEN": "top-secret-value"}[n], true }
	if got := ExpandEnvRefs(s.Environment["B"], look); got != "prefix-top-secret-value-suffix" {
		t.Fatalf("expand: %q", got)
	}
	if got := ExpandEnvRefs(s.Command[2], look); got != "echo top-secret-value" {
		t.Fatalf("expand command: %q", got)
	}
}

func TestParseComposeIgnoresProcessEnv(t *testing.T) {
	t.Setenv("BITPLOY_TEST_HOST_SECRET", "from-the-server")
	p := runComposeCase(t, composeCase{file: "services:\n  web:\n    image: nginx\n    environment:\n      X: ${BITPLOY_TEST_HOST_SECRET:-none}\n      Y:\n"})
	if got := p.Services[0].Environment["X"]; got != "none" {
		t.Fatalf("the server's environment leaked into the compose file: %q", got)
	}
	if _, ok := p.Services[0].Environment["Y"]; ok {
		t.Fatalf("a bare name without a shared variable should be left out: %v", p.Services[0].Environment)
	}
}

func TestFindComposeFile(t *testing.T) {
	files := []string{"docker-compose.yml", "compose.yaml", "api/compose.yml", "src/x.go"}
	if got := FindComposeFile(files, ".", ""); got != "compose.yaml" {
		t.Fatalf("root: %q", got)
	}
	if got := FindComposeFile(files, "api", ""); got != "api/compose.yml" {
		t.Fatalf("context dir first: %q", got)
	}
	if got := FindComposeFile(files, ".", "docker-compose.yml"); got != "docker-compose.yml" {
		t.Fatalf("explicit: %q", got)
	}
	if got := FindComposeFile(files, ".", "deploy/compose.yaml"); got != "" {
		t.Fatalf("missing explicit: %q", got)
	}
	if got := FindComposeFile([]string{"Dockerfile"}, ".", ""); got != "" {
		t.Fatalf("none: %q", got)
	}
}

func TestComposeFirstError(t *testing.T) {
	p := ParseCompose(ComposeInput{Content: []byte("services:\n  a:\n    image: nginx\n    privileged: true\n    cap_add: [ALL]\n")})
	e := p.FirstError()
	if e == nil || e.Code != CodeComposeForbidden || e.Next != "validate_compose" || e.Details["service"] != "a" {
		t.Fatalf("%+v", e)
	}
	if !strings.Contains(e.Message, "1 more") {
		t.Fatalf("the first error counts the others: %q", e.Message)
	}
}

// ${{alias.VAR}} cannot be written into a compose file; a shared variable
// that holds one works in environment and command (filled in at start) but
// not in settings fixed before that.
func TestParseComposeReferences(t *testing.T) {
	refEnv := map[string]string{"DBURL": "${{db.DATABASE_URL}}"}
	cases := []composeCase{
		{name: "in environment", file: "services:\n  web:\n    image: nginx\n    environment:\n      DATABASE_URL: ${{db.DATABASE_URL}}\n",
			errCode: CodeComposeReference, errService: "web", errKey: "environment.DATABASE_URL", errText: "set_env DATABASE_URL=${{db.DATABASE_URL}}"},
		{name: "list environment, escaped", file: "services:\n  web:\n    image: nginx\n    environment:\n      - URL=$${{ db.DATABASE_URL }}\n",
			errCode: CodeComposeReference, errService: "web", errKey: "environment[0]", errText: "set_env URL="},
		{name: "in command", file: "services:\n  web:\n    image: nginx\n    command: [\"run\", \"${{cache.REDIS_URL}}\"]\n",
			errCode: CodeComposeReference, errService: "web", errKey: "command[1]", errText: "${REDIS_URL}"},
		{name: "shared variable in environment", file: "services:\n  web:\n    image: nginx\n    environment:\n      DB: ${DBURL}\n    command: [\"x\", \"${DBURL}\"]\n",
			env: refEnv, check: func(t *testing.T, p ComposeProject) {
				if s := p.Services[0]; s.Environment["DB"] != EnvRef("DBURL") || s.Command[1] != EnvRef("DBURL") {
					t.Errorf("markers: %v %v", s.Environment, s.Command)
				}
			}},
		{name: "shared variable in build args", file: "services:\n  web:\n    build:\n      context: .\n      args:\n        DB: ${DBURL}\n",
			env: refEnv, files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\n"},
			errCode: CodeComposeReference, errService: "web", errKey: "build.args.DB", errText: "when a container starts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runComposeCase(t, c) })
	}
}

// A service gets at most what one app may have (64 GB, 64 CPUs), also for a
// user who may overallocate; a huge cpus: must not overflow int64.
func TestParseComposeResourceBounds(t *testing.T) {
	limits := func(mem, cpus string) string {
		return "services:\n  web:\n    image: nginx\n    deploy:\n      resources:\n        limits:\n          memory: " + mem + "\n          cpus: \"" + cpus + "\"\n"
	}
	cases := []composeCase{
		{name: "the most", file: limits("64g", "64"), check: func(t *testing.T, p ComposeProject) {
			if s := p.Services[0]; s.MemoryMB != MaxMemoryMB || s.CPUMillis != MaxCPUMillis {
				t.Errorf("%d MB %d", s.MemoryMB, s.CPUMillis)
			}
		}},
		{name: "too much memory", file: limits("65g", "1"),
			errCode: CodeComposeInvalid, errService: "web", errKey: "deploy.resources.limits.memory", errText: "64 GB"},
		{name: "too many cpus", file: limits("1g", "64.5"),
			errCode: CodeComposeInvalid, errService: "web", errKey: "deploy.resources.limits.cpus", errText: "64 CPUs"},
		{name: "cpus beyond int64", file: limits("1g", "1e30"),
			errCode: CodeComposeInvalid, errService: "web", errKey: "deploy.resources.limits.cpus"},
		{name: "short form", file: "services:\n  web:\n    image: nginx\n    mem_limit: 128g\n    cpus: 100\n",
			errCode: CodeComposeInvalid, errService: "web", errKey: "mem_limit", check: func(t *testing.T, p ComposeProject) {
				if len(p.Errors) != 2 || p.Errors[1].Key != "cpus" {
					t.Errorf("errors: %+v", p.Errors)
				}
			}},
		{name: "the least is raised", file: limits("1m", "0.01"), warn: []string{CodeComposeKeyIgnored}, check: func(t *testing.T, p ComposeProject) {
			if s := p.Services[0]; s.MemoryMB != composeMinMemoryMB || s.CPUMillis != composeMinCPUMillis {
				t.Errorf("%d MB %d", s.MemoryMB, s.CPUMillis)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runComposeCase(t, c) })
	}
}

// image: is shown in the deploy log and errors, which read-only tokens and
// admins can read, so a shared variable with a value cannot be filled in
// there. A default from the file is fine.
func TestParseComposeImageVariables(t *testing.T) {
	env := map[string]string{"TOKEN": "top-secret-value", "EMPTY": ""}
	cases := []composeCase{
		{name: "a set variable", file: "services:\n  web:\n    image: \"ghcr.io/acme/${TOKEN}:1\"\n", env: env,
			errCode: CodeComposeImageVariable, errService: "web", errKey: "image", errText: "TOKEN", check: func(t *testing.T, p ComposeProject) {
				for _, e := range p.Errors {
					if strings.Contains(e.Message+e.Hint, "top-secret-value") {
						t.Errorf("the error holds the value: %+v", e)
					}
				}
			}},
		{name: "a set variable with a default", file: "services:\n  web:\n    image: postgres:${TOKEN:-16}\n", env: env,
			errCode: CodeComposeImageVariable, errService: "web"},
		{name: "bare $VAR", file: "services:\n  web:\n    image: nginx:$TOKEN\n", env: env,
			errCode: CodeComposeImageVariable, errService: "web"},
		{name: "unset, default used", file: "services:\n  web:\n    image: postgres:${PG_VERSION:-16}\n", env: env,
			check: func(t *testing.T, p ComposeProject) {
				if p.Services[0].Image != "postgres:16" {
					t.Errorf("image %q", p.Services[0].Image)
				}
			}},
		{name: "empty, default used", file: "services:\n  web:\n    image: postgres:${EMPTY:-16}\n", env: env},
		{name: "a build service ignores image", file: "services:\n  web:\n    build: .\n    image: \"acme/${TOKEN}\"\n", env: env,
			files: map[string]string{"Dockerfile": "FROM node:22\nEXPOSE 3000\n"}, warn: []string{CodeComposeKeyIgnored}},
		{name: "elsewhere is fine", file: "services:\n  web:\n    image: nginx\n    environment:\n      T: ${TOKEN}\n", env: env},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runComposeCase(t, c) })
	}
}
