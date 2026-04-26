// wails-api.test.ts mocks the auto-generated bindings module so we can
// verify the wrappers in wails-api.ts faithfully forward arguments and
// unwrap the CancellablePromise return type into plain promises.
//
// These wrappers are the single seam between the React app and the Go
// service — if any binding is misnamed or argument-swapped here, every
// downstream component breaks at runtime with a confusing error. The
// tests are tiny but catch a whole class of mis-wirings.

import { describe, expect, it, vi, beforeEach } from 'vitest';

// vi.hoisted gives us shared spies that are visible inside the vi.mock
// factory below (factories run before module imports, so we can't refer to
// regular variables defined later).
const bindings = vi.hoisted(() => ({
  GetConfig: vi.fn(),
  SaveConfig: vi.fn(),
  StartProxy: vi.fn(),
  StopProxy: vi.fn(),
  GetStatus: vi.fn(),
  GetMetrics: vi.fn(),
  GetLogs: vi.fn(),
  ClearLogs: vi.fn(),
  LoadCAPEMFromFile: vi.fn(),
  ListProfiles: vi.fn(),
  SwitchProfile: vi.fn(),
  CreateProfile: vi.fn(),
  DuplicateProfile: vi.fn(),
  RenameProfile: vi.fn(),
  DeleteProfile: vi.fn(),
  TestUpstreamTLS: vi.fn(),
  TestProxyAuth: vi.fn(),
  TestCONNECT: vi.fn(),
  TestHTTPGet: vi.fn(),
}));

vi.mock('../bindings/burp-upstream-adapter/app', () => bindings);

const eventsOnSpy = vi.fn();
vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (name: string, cb: (e: { data: unknown }) => void) => {
      eventsOnSpy(name, cb);
      // Return the cleanup function the runtime would normally return.
      return () => {};
    },
  },
}));

beforeEach(() => {
  Object.values(bindings).forEach(fn => fn.mockReset());
  eventsOnSpy.mockReset();
});

describe('binding wrappers', () => {
  it('GetConfig returns the binding result as a typed promise', async () => {
    bindings.GetConfig.mockReturnValue(Promise.resolve({ active_profile: 'p1' }));
    const api = await import('./wails-api');
    const got = await api.GetConfig();
    expect(got).toEqual({ active_profile: 'p1' });
    expect(bindings.GetConfig).toHaveBeenCalledOnce();
  });

  it('SaveConfig forwards the DTO unchanged', async () => {
    bindings.SaveConfig.mockReturnValue(Promise.resolve());
    const api = await import('./wails-api');
    const dto = { active_profile: 'p1', upstream_host: 'h', upstream_port: 1, username: '', password: '', verify_tls: true, custom_ca_pem: '', connect_timeout: 1, idle_timeout: 1, bind_host: '127.0.0.1', bind_port: 1, minimize_to_tray_on_close: false, hide_dock_icon: false };
    await api.SaveConfig(dto);
    expect(bindings.SaveConfig).toHaveBeenCalledWith(dto);
  });

  it('StartProxy / StopProxy / GetStatus dispatch the matching binding', async () => {
    bindings.StartProxy.mockReturnValue(Promise.resolve());
    bindings.StopProxy.mockReturnValue(Promise.resolve());
    bindings.GetStatus.mockReturnValue(Promise.resolve('running'));

    const api = await import('./wails-api');
    await api.StartProxy();
    await api.StopProxy();
    const status = await api.GetStatus();

    expect(bindings.StartProxy).toHaveBeenCalledOnce();
    expect(bindings.StopProxy).toHaveBeenCalledOnce();
    expect(status).toBe('running');
  });

  it('GetMetrics returns a typed MetricsSnapshot', async () => {
    bindings.GetMetrics.mockReturnValue(Promise.resolve({
      active_connections: 1, total_requests: 2, bytes_in: 3, bytes_out: 4, last_error: '',
    }));
    const api = await import('./wails-api');
    const m = await api.GetMetrics();
    expect(m.total_requests).toBe(2);
  });

  it('GetLogs and ClearLogs round-trip', async () => {
    bindings.GetLogs.mockReturnValue(Promise.resolve([{ time: 't', level: 'INFO', message: 'm' }]));
    bindings.ClearLogs.mockReturnValue(Promise.resolve());
    const api = await import('./wails-api');
    const logs = await api.GetLogs();
    expect(logs).toHaveLength(1);
    expect(logs[0].level).toBe('INFO');
    await api.ClearLogs();
    expect(bindings.ClearLogs).toHaveBeenCalledOnce();
  });

  it('LoadCAPEMFromFile returns the binding string', async () => {
    bindings.LoadCAPEMFromFile.mockReturnValue(Promise.resolve('-----BEGIN-----'));
    const api = await import('./wails-api');
    expect(await api.LoadCAPEMFromFile()).toBe('-----BEGIN-----');
  });

  it('profile management bindings forward names without mangling', async () => {
    bindings.ListProfiles.mockReturnValue(Promise.resolve([{ name: 'a' }]));
    bindings.SwitchProfile.mockReturnValue(Promise.resolve({}));
    bindings.CreateProfile.mockReturnValue(Promise.resolve({}));
    bindings.DuplicateProfile.mockReturnValue(Promise.resolve({}));
    bindings.RenameProfile.mockReturnValue(Promise.resolve({}));
    bindings.DeleteProfile.mockReturnValue(Promise.resolve({}));

    const api = await import('./wails-api');
    await api.ListProfiles();
    await api.SwitchProfile('staging');
    await api.CreateProfile('new');
    await api.DuplicateProfile('src', 'dst');
    await api.RenameProfile('old', 'new');
    await api.DeleteProfile('victim');

    expect(bindings.SwitchProfile).toHaveBeenCalledWith('staging');
    expect(bindings.CreateProfile).toHaveBeenCalledWith('new');
    expect(bindings.DuplicateProfile).toHaveBeenCalledWith('src', 'dst');
    expect(bindings.RenameProfile).toHaveBeenCalledWith('old', 'new');
    expect(bindings.DeleteProfile).toHaveBeenCalledWith('victim');
  });

  it('Test* helpers forward their arguments verbatim', async () => {
    bindings.TestUpstreamTLS.mockReturnValue(Promise.resolve({ ok: true, message: '', latency: '1ms' }));
    bindings.TestProxyAuth.mockReturnValue(Promise.resolve({ ok: true, message: '', latency: '1ms' }));
    bindings.TestCONNECT.mockReturnValue(Promise.resolve({ ok: true, message: '', latency: '1ms' }));
    bindings.TestHTTPGet.mockReturnValue(Promise.resolve({ ok: true, message: '', latency: '1ms' }));

    const api = await import('./wails-api');
    await api.TestUpstreamTLS();
    await api.TestProxyAuth();
    await api.TestCONNECT('host:443');
    await api.TestHTTPGet('http://example.com/');

    expect(bindings.TestCONNECT).toHaveBeenCalledWith('host:443');
    expect(bindings.TestHTTPGet).toHaveBeenCalledWith('http://example.com/');
  });
});

describe('Events bridge', () => {
  it('EventsOn unwraps the runtime envelope and exposes only the data', async () => {
    const api = await import('./wails-api');
    const cb = vi.fn();
    api.EventsOn<{ active_connections: number }>('metrics', cb);

    expect(eventsOnSpy).toHaveBeenCalledOnce();
    const handler = eventsOnSpy.mock.calls[0][1];
    handler({ data: { active_connections: 7 } });
    expect(cb).toHaveBeenCalledWith({ active_connections: 7 });
  });

  // EventsOff cleans up handlers registered with EventsOn. The contract is
  // that calling it twice is a no-op (the existing component does this on
  // unmount) — without a registry, the second call would throw.
  it('EventsOff is idempotent', async () => {
    const api = await import('./wails-api');
    api.EventsOn('foo', () => {});
    api.EventsOff('foo');
    api.EventsOff('foo'); // no throw
  });

  it('EventsOff for an unknown event is a silent no-op', async () => {
    const api = await import('./wails-api');
    api.EventsOff('never-registered');
  });
});
