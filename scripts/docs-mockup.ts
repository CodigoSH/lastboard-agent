/**
 * Renders the pictures of the agent that come from Lastboard's screen:
 *
 *   assets/mockup.png            the image the README and the project page lead with
 *   docs/assets/docker-page.png  Lastboard's Docker page on its own, which the front
 *                                page of the documentation opens with
 *   docs/assets/docker-panel.png the same page with a container's panel open
 *
 *   bun scripts/docs-mockup.ts                 # Lastboard checked out at ../Lastboard
 *   LASTBOARD=/path/to/Lastboard bun scripts/docs-mockup.ts
 *
 * The agent has no screen of its own, so what is photographed is what it makes
 * possible: Lastboard's Docker page listing two hosts, each reached through an
 * agent built from this repository. The hosts are not real. A small Docker API
 * is served on two Unix sockets with containers invented for the picture, and
 * the agents are pointed at those sockets exactly as they would be at
 * /var/run/docker.sock — so every row on the screen went through this code, and
 * nobody's actual containers end up in a published image.
 *
 * Both programs are built before anything is photographed: a picture of the
 * interface as it was yesterday is worse than none, because nobody doubts it.
 */
import { readFileSync, mkdirSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { createServer } from 'node:net';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const OUT = 'assets/mockup.png';
const DOC_OUT = 'docs/assets/docker-page.png';
const DOC_PANEL_OUT = 'docs/assets/docker-panel.png';
const WIDTH = 1600, HEIGHT = 900, DPR = 2;
const LASTBOARD = resolve(process.env.LASTBOARD || '../Lastboard');
const LASTBOARD_BIN = join(LASTBOARD, 'bin/lastboard');
const WORK = join(tmpdir(), 'lastboard-agent-mockup');

// The house look for every published picture.
const ACCENT = '#e8520e', LANG = 'en', THEME = 'dark';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const dataUri = (path: string, mime: string) =>
    `data:${mime};base64,${readFileSync(path).toString('base64')}`;

/** A port nobody is listening on, so two runs never meet each other. */
function freePort(): Promise<number> {
    return new Promise((res, rej) => {
        const s = createServer();
        s.once('error', rej);
        s.listen(0, '127.0.0.1', () => {
            const port = (s.address() as any).port;
            s.close(() => res(port));
        });
    });
}

async function waitFor(url: string, what: string) {
    for (let i = 0; i < 80; i++) {
        try { if ((await fetch(url)).ok) return; } catch { }
        await sleep(250);
    }
    throw new Error(`${what} never answered on ${url}`);
}

// ── The two hosts ───────────────────────────────────────────────────────────

interface FakeContainer {
    name: string; image: string; running: boolean; up: string;
    ports?: number[]; cpu: number; memMiB: number;
    /** The Compose project it was started by. */
    project?: string;
    /** What its own check says, when it has one. */
    health?: 'healthy' | 'unhealthy';
    /** How a stopped one ended. */
    exit?: number;
}

interface FakeHost {
    id: string; name: string; containers: FakeContainer[];
    /** What the daemon says of its host, and what it holds on disk (bytes). */
    cpus: number; memGiB: number;
    disk: { images: number; volumes: number; cache: number };
}

const GiB = 1024 ** 3, MiB = 1024 ** 2;

const HOSTS: FakeHost[] = [
    {
        id: 'nas', name: 'Home server', cpus: 8, memGiB: 16,
        disk: { images: 11.4 * GiB, volumes: 7.6 * GiB, cache: 2.1 * GiB },
        containers: [
            { name: 'jellyfin', image: 'jellyfin/jellyfin:10.10', running: true, up: 'Up 12 days (healthy)', health: 'healthy', ports: [8096], cpu: 81.9, memMiB: 812, project: 'media' },
            { name: 'immich-server', image: 'ghcr.io/immich-app/immich-server:release', running: true, up: 'Up 12 days (healthy)', health: 'healthy', ports: [2283], cpu: 39.7, memMiB: 1240, project: 'immich' },
            { name: 'immich-postgres', image: 'postgres:16-alpine', running: true, up: 'Up 12 days (healthy)', health: 'healthy', cpu: 10.2, memMiB: 214, project: 'immich' },
            { name: 'immich-redis', image: 'redis:7-alpine', running: true, up: 'Up 12 days', cpu: 3.8, memMiB: 24, project: 'immich' },
            { name: 'vaultwarden', image: 'vaultwarden/server:1.32', running: true, up: 'Up 12 days (healthy)', health: 'healthy', ports: [8222], cpu: 2.6, memMiB: 38 },
            { name: 'syncthing', image: 'syncthing/syncthing:1.29', running: true, up: 'Up 4 days', ports: [8384], cpu: 21.8, memMiB: 96 },
            { name: 'paperless-worker', image: 'ghcr.io/paperless-ngx/paperless-ngx:2.13', running: false, up: 'Exited (137) 2 hours ago', exit: 137, cpu: 0, memMiB: 0, project: 'paperless' },
            { name: 'nextcloud', image: 'nextcloud:30-apache', running: false, up: 'Exited (0) 3 days ago', exit: 0, cpu: 0, memMiB: 0 },
        ],
    },
    {
        id: 'vps', name: 'Cloud VPS', cpus: 4, memGiB: 4,
        disk: { images: 2.3 * GiB, volumes: 0.9 * GiB, cache: 0.4 * GiB },
        containers: [
            { name: 'caddy', image: 'caddy:2-alpine', running: true, up: 'Up 31 days', ports: [443], cpu: 5.1, memMiB: 42 },
            { name: 'uptime-kuma', image: 'louislam/uptime-kuma:1', running: true, up: 'Up 31 days (healthy)', health: 'healthy', ports: [3001], cpu: 28.2, memMiB: 148 },
            { name: 'freshrss', image: 'freshrss/freshrss:1.25', running: true, up: 'Up 9 days (unhealthy)', health: 'unhealthy', ports: [8080], cpu: 3.8, memMiB: 61 },
        ],
    },
];

const idOf = (host: string, name: string) => new Bun.CryptoHasher('sha256').update(`${host}/${name}`).digest('hex');

/** A reading that moves a little with the clock, so the page's history is a line and not a ruler. */
const wobble = (base: number, name: string) => base * (1 + 0.22 * Math.sin(Date.now() / 7000 + name.length));

/**
 * The part of the Docker Engine API the agent forwards, answering for one
 * invented host. Stats carry a delta between two readings, as Docker's do, so
 * the dashboard's CPU arithmetic is the real one.
 */
function serveFakeDocker(socket: string, host: FakeHost) {
    const byId = (id: string) => host.containers.find((c) => idOf(host.id, c.name).startsWith(id));
    const started = (c: FakeContainer) => new Date(Date.now() - (parseInt(c.up.match(/\d+/)?.[0] || '1', 10) * 86400_000)).toISOString();
    return Bun.serve({
        unix: socket,
        fetch(req) {
            const url = new URL(req.url);
            const path = url.pathname;
            if (req.method === 'GET' && path === '/containers/json') {
                return Response.json(host.containers.map((c, i) => ({
                    Id: idOf(host.id, c.name),
                    Names: [`/${c.name}`],
                    Image: c.image,
                    State: c.running ? 'running' : 'exited',
                    Status: c.up,
                    Created: 1756000000 + i * 3600,
                    Ports: (c.ports || []).map((p) => ({ IP: '0.0.0.0', PrivatePort: p, PublicPort: p, Type: 'tcp' })),
                    Labels: c.project ? { 'com.docker.compose.project': c.project } : {},
                })));
            }
            if (req.method === 'GET' && path === '/info') {
                return Response.json({ ID: 'invented', Name: host.name, NCPU: host.cpus, MemTotal: Math.round(host.memGiB * GiB), KernelVersion: '6.8.0' });
            }
            if (req.method === 'GET' && path === '/system/df') {
                const n = host.containers.length;
                return Response.json({
                    LayersSize: Math.round(host.disk.images),
                    Images: Array.from({ length: n }, (_, i) => ({ Size: Math.round(host.disk.images / n), SharedSize: 0, Containers: i % 4 === 3 ? 0 : 1 })),
                    Containers: host.containers.map((c) => ({ SizeRw: c.running ? 0 : 60 * MiB, State: c.running ? 'running' : 'exited' })),
                    Volumes: [
                        { UsageData: { Size: Math.round(host.disk.volumes * 0.8), RefCount: 1 } },
                        { UsageData: { Size: Math.round(host.disk.volumes * 0.2), RefCount: 0 } },
                    ],
                    BuildCache: [{ Size: Math.round(host.disk.cache), InUse: false }],
                });
            }
            const stats = path.match(/^\/containers\/([^/]+)\/stats$/);
            if (req.method === 'GET' && stats) {
                const c = byId(stats[1]);
                if (!c) return new Response('{"message":"No such container"}', { status: 404 });
                const system = 1_000_000_000, cpus = host.cpus;
                const cpu = wobble(c.cpu, c.name);
                return Response.json({
                    cpu_stats: { cpu_usage: { total_usage: 5_000_000_000 + Math.round(cpu / 100 * system) }, system_cpu_usage: 90_000_000_000 + system * cpus, online_cpus: cpus },
                    precpu_stats: { cpu_usage: { total_usage: 5_000_000_000 }, system_cpu_usage: 90_000_000_000 },
                    memory_stats: { usage: Math.round(wobble(c.memMiB, c.name + 'm') * MiB), limit: Math.round(host.memGiB * GiB) },
                });
            }
            const one = path.match(/^\/containers\/([^/]+)\/json$/);
            if (req.method === 'GET' && one) {
                const c = byId(one[1]);
                if (!c) return new Response('{"message":"No such container"}', { status: 404 });
                return Response.json({
                    Id: idOf(host.id, c.name), Name: `/${c.name}`, Created: started(c),
                    State: {
                        Status: c.running ? 'running' : 'exited', ExitCode: c.exit ?? 0, StartedAt: started(c), FinishedAt: c.running ? '0001-01-01T00:00:00Z' : new Date(Date.now() - 7200_000).toISOString(),
                        ...(c.health ? { Health: { Status: c.health } } : {}),
                    },
                    RestartCount: 0,
                    Config: { Image: c.image, Env: ['TZ=Europe/Lisbon', 'PUID=1000'], Tty: true },
                    HostConfig: { RestartPolicy: { Name: 'unless-stopped' } },
                    Mounts: c.name === 'jellyfin' ? [{ Type: 'bind', Source: '/srv/media', Destination: '/media', RW: false }, { Type: 'volume', Name: 'jellyfin-config', Source: '/var/lib/docker/volumes/jellyfin-config/_data', Destination: '/config', RW: true }] : [{ Type: 'volume', Name: `${c.name}-data`, Source: `/var/lib/docker/volumes/${c.name}-data/_data`, Destination: '/data', RW: true }],
                    NetworkSettings: {
                        Ports: Object.fromEntries((c.ports || []).map((p) => [`${p}/tcp`, [{ HostIp: '0.0.0.0', HostPort: String(p) }]])),
                        Networks: { [c.project ? `${c.project}_default` : 'bridge']: { IPAddress: '172.18.0.' + (2 + host.containers.indexOf(c)) } },
                    },
                });
            }
            const logs = path.match(/^\/containers\/([^/]+)\/logs$/);
            if (req.method === 'GET' && logs) {
                const c = byId(logs[1]);
                if (!c) return new Response('{"message":"No such container"}', { status: 404 });
                const now = Date.now();
                const lines = [
                    'starting up', `listening on :${(c.ports || [8080])[0]}`, 'loaded configuration from /config', 'database is ready',
                    'GET /health 200 2ms', 'GET /api/status 200 4ms', 'GET /health 200 1ms',
                    ...(c.health === 'unhealthy' ? ['health check failed: connection refused', 'health check failed: connection refused'] : ['GET /health 200 2ms']),
                    ...(c.exit === 137 ? ['worker killed: out of memory'] : []),
                ];
                const body = lines.map((l, i) => `${new Date(now - (lines.length - i) * 20_000).toISOString()} ${l}`).join('\n') + '\n';
                return new Response(body, { headers: { 'Content-Type': 'text/plain' } });
            }
            if (req.method === 'POST' && /^\/containers\/[^/]+\/(start|stop|restart)$/.test(path)) {
                return new Response(null, { status: 204 });
            }
            return new Response('{"message":"page not found"}', { status: 404 });
        },
    });
}

// ── The browser ─────────────────────────────────────────────────────────────

let id = 0;
const pending = new Map<number, (v: any) => void>();
let ws: WebSocket;
function cdp(method: string, params: any = {}): Promise<any> {
    const myId = ++id;
    return new Promise((resolve) => { pending.set(myId, resolve); ws.send(JSON.stringify({ id: myId, method, params })); });
}

async function evaluate(expression: string) {
    const r = await cdp('Runtime.evaluate', { expression: `(async () => { ${expression} })()`, awaitPromise: true, returnByValue: true });
    if (r.result?.exceptionDetails) throw new Error(r.result.exceptionDetails.exception?.description || 'evaluate failed');
    return r.result?.result?.value;
}

/**
 * An element of the dashboard, searched through every shadow root rather than
 * along one fixed path, so a change in how the page nests its parts still finds it.
 */
const FIND = `
    const find = (root, sel) => {
        const hit = root.querySelector(sel);
        if (hit) return hit;
        for (const el of root.querySelectorAll('*')) {
            if (el.shadowRoot) { const deep = find(el.shadowRoot, sel); if (deep) return deep; }
        }
        return null;
    };`;

/** Opens the Docker page from the top bar, as a person would, and leaves it on its first tab. */
async function openDockerPage() {
    await evaluate(`
        ${FIND}
        for (let i = 0; i < 60 && !find(document, '[title="Docker"]'); i++) await new Promise((r) => setTimeout(r, 250));
        const button = find(document, '[title="Docker"]');
        if (!button) throw new Error('no Docker button in the top bar — are the hosts saved?');
        (button.closest('button') || button).click();`);
}

/** The tab with this id, clicked. */
async function openTab(id: string) {
    await evaluate(`
        ${FIND}
        const tab = find(document, 'docker-page').shadowRoot.querySelector('#tabs [data-tab="${id}"]');
        if (!tab) throw new Error('no tab ${id} on the Docker page');
        tab.click();`);
}

/**
 * Waits until the page shows what the picture needs — a condition, not a time.
 * Not getting there is a failure with a name and a screenshot of what was on
 * screen, never a picture taken anyway.
 */
async function waitFor_(what: string, condition: string) {
    let last: any = null;
    for (let i = 0; i < 160; i++) {
        last = await evaluate(`
            ${FIND}
            const root = find(document, 'docker-page')?.shadowRoot;
            if (!root) return { ok: false, why: 'no Docker page on the screen' };
            ${condition}`);
        if (last?.ok) return;
        await sleep(250);
    }
    const shot = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(join(WORK, 'failed.png'), Buffer.from(shot.result.data, 'base64'));
    throw new Error(`${what} never settled: ${JSON.stringify(last)} — what was on screen is in ${join(WORK, 'failed.png')}`);
}

/** The overview: four indicators with their figures, the session's chart drawn, and the attention list. */
const overviewReady = (rows: number) => waitFor_('the overview', `
    const kpis = [...root.querySelectorAll('.kpi__v')].map((e) => e.textContent.trim());
    const chart = !!root.querySelector('.chart svg path');
    const attention = root.querySelectorAll('.attn__row').length;
    return { ok: kpis.length === 4 && kpis.every((k) => k && k !== '—') && chart && attention > 0, kpis, chart, attention };`);

/** The list: every container is a card, and every running one has its figures. */
const listReady = (rows: number) => waitFor_('the container list', `
    const cards = root.querySelectorAll('.unit').length;
    const loading = root.querySelectorAll('.unit .is-loading, .unit [class*="loading"]').length;
    return { ok: cards === ${rows} && loading === 0, cards, loading };`);

/**
 * One screen. `touch` is a tablet: touch input at a width where the dashboard
 * still lays out in full (below 768px it shows bookmarks only).
 */
async function shoot(app: string, w: number, h: number, dpr: number, touch: boolean, tab: 'overview' | 'containers', rows: number, out: string) {
    await cdp('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: dpr, mobile: touch });
    await cdp('Emulation.setTouchEmulationEnabled', { enabled: touch, maxTouchPoints: touch ? 5 : 0 });
    await cdp('Page.navigate', { url: app });
    await sleep(1500);
    await openDockerPage();
    await overviewReady(rows);
    if (tab === 'containers') { await openTab('containers'); await listReady(rows); }
    await evaluate(`await document.fonts.ready;`);
    await sleep(900); // the entrance animation of the page
    const png = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(out, Buffer.from(png.result.data, 'base64'));
    console.log(`  ✓ ${touch ? 'tablet' : 'laptop'} ${tab} ${w}x${h}@${dpr}`);
}

/** The page with one container's panel open on its first tab. */
async function shootPanel(app: string, rows: number, container: string, out: string) {
    await cdp('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
    await cdp('Emulation.setTouchEmulationEnabled', { enabled: false, maxTouchPoints: 0 });
    await cdp('Page.navigate', { url: app });
    await sleep(1500);
    await openDockerPage();
    await overviewReady(rows);
    await openTab('containers');
    await listReady(rows);
    await evaluate(`
        ${FIND}
        const root = find(document, 'docker-page').shadowRoot;
        const card = [...root.querySelectorAll('.unit')].find((u) => u.textContent.includes('${container}'));
        if (!card) throw new Error('no card for ${container}');
        card.click();`);
    await waitFor_('the container panel', `
        const on = root.getElementById('drawer')?.classList.contains('on');
        const facts = root.querySelectorAll('#dr-body .fact, #dr-body .facts > *').length;
        const figures = [...root.querySelectorAll('#dr-body .kpi__v, #dr-body .live3 b, #dr-body .card b')].some((b) => /%/.test(b.textContent));
        return { ok: on && facts > 0 && figures, on, facts, figures };`);
    await sleep(900);
    const png = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(out, Buffer.from(png.result.data, 'base64'));
    console.log(`  ✓ ${out} 1440x900`);
}

// ── The run ─────────────────────────────────────────────────────────────────

async function main() {
    console.log('· building the agent and Lastboard, so the picture is of this code');
    rmSync(WORK, { recursive: true, force: true });
    mkdirSync(WORK, { recursive: true });
    const agentBin = join(WORK, 'lastboard-agent');
    if (spawnSync('go', ['build', '-ldflags', '-X main.version=mockup', '-o', agentBin, '.'], { stdio: 'inherit' }).status !== 0) {
        throw new Error('go build failed');
    }
    if (!existsSync(join(LASTBOARD, 'Makefile'))) throw new Error(`no Lastboard checkout at ${LASTBOARD} — set LASTBOARD`);
    if (spawnSync('make', ['build'], { cwd: LASTBOARD, stdio: 'inherit' }).status !== 0) {
        throw new Error('make build failed in the Lastboard checkout');
    }

    const procs: ChildProcess[] = [];
    const closers: (() => void)[] = [];
    const stop = () => {
        procs.forEach((p) => { try { p.kill(); } catch { } });
        closers.forEach((c) => { try { c(); } catch { } });
    };
    process.on('exit', stop);
    for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP'] as const) process.on(sig, () => { stop(); process.exit(1); });

    // Two invented hosts, each behind its own agent.
    const agents: { host: typeof HOSTS[number]; port: number; token: string }[] = [];
    for (const host of HOSTS) {
        const socket = join(WORK, `${host.id}.sock`);
        const daemon = serveFakeDocker(socket, host);
        closers.push(() => daemon.stop(true));
        const port = await freePort();
        const token = Array.from(crypto.getRandomValues(new Uint8Array(32)), (b) => b.toString(16).padStart(2, '0')).join('');
        procs.push(spawn(agentBin, [], { env: { ...process.env, TOKEN: token, PORT: String(port), SOCKET_PATH: socket }, stdio: 'ignore' }));
        await waitFor(`http://127.0.0.1:${port}/health`, `the agent for ${host.name}`);
        agents.push({ host, port, token });
    }

    // A throwaway Lastboard whose account has the two hosts, as it would after
    // Settings → Docker, and a board with only a clock on it.
    const lbPort = await freePort();
    const app = `http://localhost:${lbPort}/`;
    const db = join(WORK, 'lastboard.db');
    procs.push(spawn(LASTBOARD_BIN, [], { env: { ...process.env, DB_FILE: db, PORT: String(lbPort) }, stdio: 'ignore' }));
    await waitFor(app, 'Lastboard');

    await fetch(`${app}api/setup`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'demo', password: 'Lastboard123!', language: LANG, theme: THEME, accent_color: ACCENT }),
    });
    const login = await fetch(`${app}api/login`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'demo', password: 'Lastboard123!' }),
    });
    const cookies = Object.fromEntries((login.headers.getSetCookie?.() || []).map((c) => c.split(';')[0].split('=')));
    if (!cookies.session_token || !cookies.csrf_token) throw new Error('could not sign in to the throwaway Lastboard');

    const saved = await fetch(`${app}api/docker/settings`, {
        method: 'POST',
        // A write from a page carries the session and echoes the CSRF token, as the page itself does.
        headers: { 'Content-Type': 'application/json', Cookie: `session_token=${cookies.session_token}; csrf_token=${cookies.csrf_token}`, 'X-CSRF-Token': cookies.csrf_token },
        body: JSON.stringify({
            hosts: agents.map(({ host, port, token }) => ({ id: host.id, name: host.name, type: 'agent', url: `127.0.0.1:${port}`, token, enabled: true })),
        }),
    });
    if (!saved.ok) throw new Error(`saving the Docker hosts failed: ${saved.status} ${await saved.text()}`);

    const { Database } = await import('bun:sqlite');
    const sql = new Database(db);
    sql.run(`DELETE FROM items`);
    sql.run(`INSERT INTO dashboards (user_id, name, position, is_default) VALUES (1, 'Main dashboard', 0, 1)`);
    const board = (sql.query(`SELECT id FROM dashboards WHERE is_default = 1`).get() as { id: number }).id;
    sql.run(`INSERT INTO items (user_id, type, x, y, w, h, content, dashboard_id) VALUES (1, 'widget', 1, 1, 4, 2, ?, ?)`,
        [JSON.stringify({ widgetId: 'clock', city: 'Lisbon', timezone: 'Europe/Lisbon', hour12: false, showDate: true, showSeconds: false, showCity: true }), board]);
    sql.close();

    const expected = HOSTS.reduce((n, h) => n + h.containers.length, 0);

    procs.push(spawn('google-chrome', [
        '--headless=new', `--remote-debugging-port=${await freePort().then((p) => { (globalThis as any).CDP = p; return p; })}`,
        `--user-data-dir=${join(WORK, 'profile')}`, '--no-first-run', '--hide-scrollbars',
        '--force-color-profile=srgb', 'about:blank',
    ], { stdio: 'ignore' }));
    const CDP = (globalThis as any).CDP as number;
    await waitFor(`http://127.0.0.1:${CDP}/json/version`, 'Chrome');
    const list = await (await fetch(`http://127.0.0.1:${CDP}/json/list`)).json();
    ws = new WebSocket(list.find((t: any) => t.type === 'page').webSocketDebuggerUrl);
    await new Promise((r) => { ws.onopen = () => r(null); });
    ws.onmessage = (e) => {
        const m = JSON.parse(e.data as string);
        if (m.id && pending.has(m.id)) { pending.get(m.id)!(m); pending.delete(m.id); }
    };
    await cdp('Page.enable'); await cdp('Runtime.enable'); await cdp('Network.enable');
    await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    for (const [name, value] of Object.entries(cookies)) {
        await cdp('Network.setCookie', { name, value, domain: 'localhost', path: '/' });
    }

    console.log('▶ photographing the Docker page…');
    const desktop = join(WORK, 'desktop.png'), tablet = join(WORK, 'tablet.png');
    await shoot(app, 1280, 800, 2, false, 'overview', expected, desktop);
    await shoot(app, 1180, 820, 2, true, 'containers', expected, tablet);
    await shoot(app, 1440, 900, 1, false, 'overview', expected, DOC_OUT);
    await shootPanel(app, expected, 'jellyfin', DOC_PANEL_OUT);

    // The screens are inlined rather than linked: a file:// page that goes
    // looking for them mid-render is a composition that sometimes ships with
    // empty frames.
    const html = readFileSync('scripts/docs-mockup.html', 'utf8')
        .replace('__LOGO__', dataUri('assets/logo.png', 'image/png'))
        .replace('__DESKTOP__', dataUri(desktop, 'image/png'))
        .replace('__TABLET__', dataUri(tablet, 'image/png'));
    const page = join(WORK, 'mockup.html');
    writeFileSync(page, html);

    await cdp('Emulation.setTouchEmulationEnabled', { enabled: false, maxTouchPoints: 0 });
    await cdp('Emulation.setDeviceMetricsOverride', { width: WIDTH, height: HEIGHT, deviceScaleFactor: DPR, mobile: false });
    await cdp('Page.navigate', { url: `file://${page}` });
    await sleep(800);
    await evaluate(`await document.fonts.ready;`);
    await sleep(300);
    const png = await cdp('Page.captureScreenshot', { format: 'png' });
    writeFileSync(OUT, Buffer.from(png.result.data, 'base64'));
    console.log(`✓ ${OUT} ${WIDTH * DPR}x${HEIGHT * DPR}`);
    process.exit(0);
}

main().catch((err) => { console.error('✗', err.message); process.exit(1); });
