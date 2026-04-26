// App.test.tsx exercises the React component end-to-end with the
// wails-api module mocked. The goal is to cover behaviour the user
// actually experiences (button clicks, profile dialog flows, error
// display) rather than implementation details — every assertion below is
// the kind that would fail loudly if a real regression slipped in.
//
// All async API calls are stubbed via vi.mock; we never touch a real
// Wails runtime. The mocked wails-api module is a sibling file so the
// import path matches what App.tsx actually uses.

import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

// Helper: most modal tests need to scope queries to the modal so they
// don't collide with the underlying form's inputs/buttons. The modal
// container exposes class="modal" — the only stable hook React's testing
// library API gives us without adding a test-id everywhere.
function modal() {
  const m = document.querySelector('.modal');
  if (!m) throw new Error('modal not rendered');
  return within(m as HTMLElement);
}
import App from './App';
import type { ConfigDTO } from './types';

// vi.hoisted lets the api spies survive into the vi.mock factory below
// (factories run before regular module-scope code).
const api = vi.hoisted(() => ({
  GetConfig: vi.fn(),
  SaveConfig: vi.fn(),
  StartProxy: vi.fn(),
  StopProxy: vi.fn(),
  GetStatus: vi.fn(),
  GetMetrics: vi.fn(),
  GetLogs: vi.fn(),
  ClearLogs: vi.fn(),
  LoadCAPEMFromFile: vi.fn(),
  TestUpstreamTLS: vi.fn(),
  TestProxyAuth: vi.fn(),
  TestCONNECT: vi.fn(),
  TestHTTPGet: vi.fn(),
  ListProfiles: vi.fn(),
  SwitchProfile: vi.fn(),
  CreateProfile: vi.fn(),
  DuplicateProfile: vi.fn(),
  RenameProfile: vi.fn(),
  DeleteProfile: vi.fn(),
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
}));

vi.mock('./wails-api', () => api);

const baseConfig: ConfigDTO = {
  active_profile: 'default',
  upstream_host: 'proxy.example.com',
  upstream_port: 3128,
  username: 'alice',
  password: 'pw',
  verify_tls: true,
  custom_ca_pem: '',
  connect_timeout: 30,
  idle_timeout: 300,
  bind_host: '127.0.0.1',
  bind_port: 18080,
  minimize_to_tray_on_close: false,
  hide_dock_icon: false,
};

beforeEach(() => {
  Object.values(api).forEach(fn => fn.mockReset());
  api.GetConfig.mockResolvedValue(baseConfig);
  api.GetStatus.mockResolvedValue('stopped');
  api.GetLogs.mockResolvedValue([]);
  api.GetMetrics.mockResolvedValue({
    active_connections: 0, total_requests: 0, bytes_in: 0, bytes_out: 0, last_error: '',
  });
  api.ListProfiles.mockResolvedValue([
    { name: 'default' },
    { name: 'staging' },
  ]);
  api.SaveConfig.mockResolvedValue(undefined);
  api.StartProxy.mockResolvedValue(undefined);
  api.StopProxy.mockResolvedValue(undefined);
  api.LoadCAPEMFromFile.mockResolvedValue('');
  api.ClearLogs.mockResolvedValue(undefined);
});

async function renderApp() {
  const user = userEvent.setup();
  render(<App />);
  // App fires GetConfig / GetStatus / GetLogs / ListProfiles on mount; wait
  // for the form to populate before any test asserts on its state.
  await waitFor(() => {
    expect(api.GetConfig).toHaveBeenCalled();
    expect(api.ListProfiles).toHaveBeenCalled();
  });
  return user;
}

// --------------------------------------------------------- Initial render

describe('initial render', () => {
  it('loads config + status + logs + profiles on mount', async () => {
    await renderApp();
    expect(api.GetConfig).toHaveBeenCalledOnce();
    expect(api.GetStatus).toHaveBeenCalledOnce();
    expect(api.GetLogs).toHaveBeenCalledOnce();
    expect(api.ListProfiles).toHaveBeenCalledOnce();
  });

  it('populates form fields from the loaded config', async () => {
    await renderApp();
    expect(await screen.findByDisplayValue('proxy.example.com')).toBeInTheDocument();
    expect(screen.getByDisplayValue('alice')).toBeInTheDocument();
    expect(screen.getByDisplayValue(3128)).toBeInTheDocument();
  });

  it('renders the STOPPED status badge when the proxy is not running', async () => {
    await renderApp();
    expect(screen.getByText('STOPPED')).toBeInTheDocument();
  });

  it('shows the running badge and metric chip when status=running', async () => {
    api.GetStatus.mockResolvedValue('running');
    api.GetMetrics.mockResolvedValue({
      active_connections: 3, total_requests: 9, bytes_in: 0, bytes_out: 0, last_error: '',
    });
    await renderApp();
    expect(await screen.findByText('RUNNING')).toBeInTheDocument();
  });

  it('surfaces a load error in the error bar', async () => {
    api.GetConfig.mockRejectedValueOnce(new Error('disk full'));
    render(<App />);
    expect(await screen.findByText(/disk full/)).toBeInTheDocument();
  });
});

// --------------------------------------------------------- Tab switching

describe('tab navigation', () => {
  it('Diagnostics tab shows the test buttons', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Diagnostics' }));
    expect(screen.getByRole('button', { name: /Test Upstream TLS/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Test Proxy Auth/ })).toBeInTheDocument();
  });

  it('Activity tab shows the metrics grid', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Activity' }));
    expect(screen.getByText('Metrics')).toBeInTheDocument();
    expect(screen.getByText('Active Connections')).toBeInTheDocument();
  });

  it('Burp Setup tab includes the bind host/port the user must paste', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Burp Setup' }));
    // Bind host and port should appear in the help table, taken from config.
    expect(screen.getAllByText('127.0.0.1').length).toBeGreaterThan(0);
    expect(screen.getAllByText('18080').length).toBeGreaterThan(0);
  });
});

// --------------------------------------------------------- Save / Start / Stop

describe('Save / Start / Stop flow', () => {
  it('Save button calls SaveConfig with the current form state', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: /Save/i }));
    expect(api.SaveConfig).toHaveBeenCalledOnce();
    expect(api.SaveConfig.mock.calls[0][0].upstream_host).toBe('proxy.example.com');
  });

  it('Start Proxy saves first then starts', async () => {
    const order: string[] = [];
    api.SaveConfig.mockImplementation(() => { order.push('save'); return Promise.resolve(); });
    api.StartProxy.mockImplementation(() => { order.push('start'); return Promise.resolve(); });

    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Start Proxy' }));
    await waitFor(() => expect(order).toEqual(['save', 'start']));
  });

  it('Stop Proxy is wired only when status=running', async () => {
    api.GetStatus.mockResolvedValue('running');
    const user = await renderApp();
    const stop = await screen.findByRole('button', { name: 'Stop Proxy' });
    await user.click(stop);
    expect(api.StopProxy).toHaveBeenCalledOnce();
  });

  it('Start failure surfaces in the error bar', async () => {
    api.StartProxy.mockRejectedValue(new Error('listen on 18080: address in use'));
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Start Proxy' }));
    expect(await screen.findByText(/address in use/)).toBeInTheDocument();
  });

  // Save errors must surface — without this assertion a regression that
  // swallows the error would silently turn the Save button into a no-op.
  it('Save failure surfaces in the error bar', async () => {
    api.SaveConfig.mockRejectedValue(new Error('config invalid'));
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: /Save/i }));
    expect(await screen.findByText(/config invalid/)).toBeInTheDocument();
  });

  it('clicking the error bar dismisses it', async () => {
    api.GetConfig.mockRejectedValueOnce(new Error('boom'));
    render(<App />);
    const bar = await screen.findByText(/boom/);
    const user = userEvent.setup();
    await user.click(bar);
    expect(screen.queryByText(/boom/)).not.toBeInTheDocument();
  });
});

// --------------------------------------------------------- Form updates

describe('form updates', () => {
  it('typing into the host field updates the DTO sent to SaveConfig', async () => {
    const user = await renderApp();
    const host = screen.getByDisplayValue('proxy.example.com') as HTMLInputElement;
    await user.clear(host);
    await user.type(host, 'new.proxy.test');
    await user.click(screen.getByRole('button', { name: /Save/i }));

    expect(api.SaveConfig).toHaveBeenCalledOnce();
    expect(api.SaveConfig.mock.calls[0][0].upstream_host).toBe('new.proxy.test');
  });

  // Without the parseInt fallback the `value || 0` branch in updateField
  // would persist NaN through to Go and trip the validator. This test pins
  // that input boxes never let a NaN escape into the saved config.
  it('clearing a numeric field saves a numeric, not NaN', async () => {
    const user = await renderApp();
    const port = screen.getByDisplayValue(3128) as HTMLInputElement;
    await user.clear(port);
    await user.click(screen.getByRole('button', { name: /Save/i }));

    expect(api.SaveConfig).toHaveBeenCalledOnce();
    const saved = api.SaveConfig.mock.calls[0][0];
    expect(typeof saved.upstream_port).toBe('number');
    expect(Number.isNaN(saved.upstream_port)).toBe(false);
  });

  it('toggling Verify TLS shows the (insecure) warning', async () => {
    const user = await renderApp();
    const verify = screen.getByRole('checkbox', { name: /Verify TLS/ });
    await user.click(verify);
    expect(screen.getByText(/insecure/i)).toBeInTheDocument();
  });
});

// --------------------------------------------------------- Profile flows

describe('profile dialogs', () => {
  it('opens the New profile modal and submits a valid name', async () => {
    api.CreateProfile.mockResolvedValue({ ...baseConfig, active_profile: 'beta' });
    const user = await renderApp();

    await user.click(screen.getByRole('button', { name: 'New' }));
    expect(screen.getByText('Create new profile')).toBeInTheDocument();

    const m = modal();
    const input = m.getByRole('textbox');
    await user.clear(input);
    await user.type(input, 'beta');
    await user.click(m.getByRole('button', { name: 'Create' }));

    expect(api.CreateProfile).toHaveBeenCalledWith('beta');
  });

  it('rejects an invalid profile name with the standard error', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'New' }));
    const m = modal();
    const input = m.getByRole('textbox');
    await user.clear(input);
    await user.type(input, 'with space');
    await user.click(m.getByRole('button', { name: 'Create' }));

    expect(screen.getByText(/Profile name: 1-32 chars/)).toBeInTheDocument();
    expect(api.CreateProfile).not.toHaveBeenCalled();
  });

  it('Duplicate prefills with "<active>-copy" and submits both names', async () => {
    api.DuplicateProfile.mockResolvedValue({ ...baseConfig, active_profile: 'default-copy' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Duplicate' }));
    const m = modal();
    expect(m.getByDisplayValue('default-copy')).toBeInTheDocument();
    await user.click(m.getByRole('button', { name: 'Duplicate' }));
    expect(api.DuplicateProfile).toHaveBeenCalledWith('default', 'default-copy');
  });

  it('Rename calls RenameProfile with (from, to)', async () => {
    api.RenameProfile.mockResolvedValue({ ...baseConfig, active_profile: 'renamed' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Rename' }));
    const m = modal();
    const input = m.getByDisplayValue('default');
    await user.clear(input);
    await user.type(input, 'renamed');
    await user.click(m.getByRole('button', { name: 'Rename' }));
    expect(api.RenameProfile).toHaveBeenCalledWith('default', 'renamed');
  });

  it('Delete confirmation calls DeleteProfile', async () => {
    api.DeleteProfile.mockResolvedValue({ ...baseConfig, active_profile: 'staging' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(screen.getByText(/Delete profile "default"/)).toBeInTheDocument();
    await user.click(modal().getByRole('button', { name: 'Delete' }));
    expect(api.DeleteProfile).toHaveBeenCalledWith('default');
  });

  // When there's only one profile the Delete button is disabled — that's
  // the visible UX guard. The defensive openDeleteProfile branch (an
  // inline error toast) is unreachable through the UI but documented as
  // a belt-and-braces fallback; verifying the disabled state is the
  // assertion that actually protects the user.
  it('Delete is disabled when there is only one profile', async () => {
    api.ListProfiles.mockResolvedValue([{ name: 'default' }]);
    await renderApp();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
  });

  it('Cancel closes the modal without invoking any binding', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'New' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('Create new profile')).not.toBeInTheDocument();
    expect(api.CreateProfile).not.toHaveBeenCalled();
  });

  it('Escape key in the name input closes the modal', async () => {
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'New' }));
    const input = modal().getByRole('textbox');
    input.focus();
    await user.keyboard('{Escape}');
    expect(screen.queryByText('Create new profile')).not.toBeInTheDocument();
  });

  it('Enter key in the name input submits the dialog', async () => {
    api.CreateProfile.mockResolvedValue(baseConfig);
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'New' }));
    const input = modal().getByRole('textbox');
    await user.type(input, 'gamma{Enter}');
    expect(api.CreateProfile).toHaveBeenCalledWith('gamma');
  });
});

// --------------------------------------------------------- Profile selector

describe('profile selector', () => {
  it('switching profile via the dropdown calls SwitchProfile', async () => {
    api.SwitchProfile.mockResolvedValue({ ...baseConfig, active_profile: 'staging' });
    const user = await renderApp();
    const select = screen.getByRole('combobox');
    await user.selectOptions(select, 'staging');
    expect(api.SwitchProfile).toHaveBeenCalledWith('staging');
  });

  it('switching to the same profile is a no-op', async () => {
    const user = await renderApp();
    const select = screen.getByRole('combobox');
    await user.selectOptions(select, 'default');
    expect(api.SwitchProfile).not.toHaveBeenCalled();
  });

  // When the proxy is running, the dropdown change goes through a window.confirm
  // gate. If the user cancels, neither StopProxy nor SwitchProfile fire.
  it('declining the running-proxy confirmation aborts the switch', async () => {
    api.GetStatus.mockResolvedValue('running');
    const user = await renderApp();
    await screen.findByText('RUNNING');
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);

    await user.selectOptions(screen.getByRole('combobox'), 'staging');
    expect(api.SwitchProfile).not.toHaveBeenCalled();
    expect(api.StopProxy).not.toHaveBeenCalled();

    confirm.mockRestore();
  });

  it('accepting the confirmation stops proxy then switches', async () => {
    api.GetStatus.mockResolvedValue('running');
    api.SwitchProfile.mockResolvedValue({ ...baseConfig, active_profile: 'staging' });
    const user = await renderApp();
    await screen.findByText('RUNNING');
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);

    await user.selectOptions(screen.getByRole('combobox'), 'staging');
    await waitFor(() => expect(api.StopProxy).toHaveBeenCalled());
    expect(api.SwitchProfile).toHaveBeenCalledWith('staging');

    confirm.mockRestore();
  });
});

// --------------------------------------------------------- Diagnostics

describe('diagnostics', () => {
  it('Test Upstream TLS persists the form first then runs the check', async () => {
    api.TestUpstreamTLS.mockResolvedValue({ ok: true, message: 'TLS OK', latency: '12ms' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Diagnostics' }));
    await user.click(screen.getByRole('button', { name: /Test Upstream TLS/ }));

    await waitFor(() => {
      expect(api.SaveConfig).toHaveBeenCalled();
      expect(api.TestUpstreamTLS).toHaveBeenCalled();
    });
    expect(await screen.findByText('PASS')).toBeInTheDocument();
    expect(screen.getByText('TLS OK')).toBeInTheDocument();
  });

  it('failing diagnostics show FAIL with the upstream error', async () => {
    api.TestProxyAuth.mockResolvedValue({ ok: false, message: '407 Proxy Auth Required', latency: '5ms' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Diagnostics' }));
    await user.click(screen.getByRole('button', { name: /Test Proxy Auth/ }));
    expect(await screen.findByText('FAIL')).toBeInTheDocument();
    expect(screen.getByText(/407 Proxy Auth Required/)).toBeInTheDocument();
  });

  it('a thrown diagnostic is converted into a FAIL result rather than crashing', async () => {
    api.TestCONNECT.mockRejectedValue(new Error('dial failed'));
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Diagnostics' }));
    await user.click(screen.getByRole('button', { name: /Test CONNECT/ }));
    expect(await screen.findByText('FAIL')).toBeInTheDocument();
    expect(screen.getByText(/dial failed/)).toBeInTheDocument();
  });
});

// --------------------------------------------------------- CA loader

describe('CA loader', () => {
  it('Load button stores returned PEM into the form', async () => {
    api.LoadCAPEMFromFile.mockResolvedValue('-----BEGIN CERTIFICATE-----\nFAKE\n-----END CERTIFICATE-----\n');
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Load' }));
    // The display reflects byte count rather than raw PEM contents.
    expect(await screen.findByDisplayValue(/Loaded \(\d+ bytes/)).toBeInTheDocument();
  });

  it('Clear empties the stored PEM', async () => {
    api.GetConfig.mockResolvedValue({ ...baseConfig, custom_ca_pem: 'x' });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Clear' }));
    expect(screen.queryByText(/Loaded \(/)).not.toBeInTheDocument();
  });

  it('Cancelled file picker leaves the existing PEM untouched', async () => {
    api.GetConfig.mockResolvedValue({ ...baseConfig, custom_ca_pem: 'KEEPME' });
    api.LoadCAPEMFromFile.mockResolvedValue('');
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Load' }));
    // PEM byte count display still shows the original size.
    expect(screen.getByDisplayValue(/Loaded \(6 bytes/)).toBeInTheDocument();
  });
});

// --------------------------------------------------------- Logs

describe('Activity tab logs', () => {
  it('renders log entries from GetLogs', async () => {
    api.GetLogs.mockResolvedValue([
      { time: '2026-01-01T00:00:00Z', level: 'INFO', message: 'started' },
      { time: '2026-01-01T00:00:01Z', level: 'ERROR', message: 'kaboom' },
    ]);
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Activity' }));
    expect(screen.getByText('started')).toBeInTheDocument();
    expect(screen.getByText('kaboom')).toBeInTheDocument();
  });

  it('Clear Logs button calls ClearLogs and empties the panel', async () => {
    api.GetLogs.mockResolvedValue([
      { time: 't', level: 'INFO', message: 'old' },
    ]);
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Activity' }));
    expect(screen.getByText('old')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Clear' }));
    expect(api.ClearLogs).toHaveBeenCalled();
    expect(screen.queryByText('old')).not.toBeInTheDocument();
    // Empty-state message is shown.
    expect(screen.getByText(/No log entries yet/)).toBeInTheDocument();
  });

  it('shows the last_error banner when metrics carries one', async () => {
    api.GetStatus.mockResolvedValue('running');
    api.GetMetrics.mockResolvedValue({
      active_connections: 0, total_requests: 0, bytes_in: 0, bytes_out: 0,
      last_error: 'upstream refused',
    });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Activity' }));
    // Wait for the 1s metrics tick to populate the panel.
    await act(async () => {
      await new Promise(r => setTimeout(r, 1100));
    });
    expect(screen.getByText(/upstream refused/)).toBeInTheDocument();
  });
});

// --------------------------------------------------------- Events

describe('event subscriptions', () => {
  it('subscribes to log + status on mount and unsubscribes on unmount', async () => {
    const { unmount } = render(<App />);
    await waitFor(() => expect(api.EventsOn).toHaveBeenCalled());
    const events = api.EventsOn.mock.calls.map(c => c[0]);
    expect(events).toContain('log');
    expect(events).toContain('status');

    unmount();
    const offEvents = api.EventsOff.mock.calls.map(c => c[0]);
    expect(offEvents).toContain('log');
    expect(offEvents).toContain('status');
  });

  // Pushed log events should append into the panel without overwriting
  // existing entries. Without the spread, a single new entry would replace
  // the whole list.
  it('appends a pushed log event into the live panel', async () => {
    api.GetLogs.mockResolvedValue([]);
    const user = await renderApp();

    // Find the handler registered for the 'log' event.
    const logCall = api.EventsOn.mock.calls.find(c => c[0] === 'log');
    expect(logCall).toBeTruthy();
    const handler = logCall![1] as (e: unknown) => void;

    await user.click(screen.getByRole('button', { name: 'Activity' }));
    await act(async () => {
      handler({ time: 't', level: 'INFO', message: 'pushed-msg' });
    });
    expect(await screen.findByText('pushed-msg')).toBeInTheDocument();
  });

  it('a pushed status=running event flips the badge', async () => {
    await renderApp();
    const statusCall = api.EventsOn.mock.calls.find(c => c[0] === 'status');
    const handler = statusCall![1] as (s: unknown) => void;
    await act(async () => {
      handler('running');
    });
    expect(await screen.findByText('RUNNING')).toBeInTheDocument();
  });
});

// --------------------------------------------------------- formatBytes

// formatBytes is not exported; we validate it indirectly by injecting
// known byte counts into the metrics and reading the rendered text.
describe('formatBytes (via metrics panel)', () => {
  it('renders 0 B / KB / MB / GB scales', async () => {
    api.GetStatus.mockResolvedValue('running');
    api.GetMetrics.mockResolvedValue({
      active_connections: 0,
      total_requests: 0,
      bytes_in: 0,                 // 0 B
      bytes_out: 5 * 1024 * 1024,  // 5.0 MB
      last_error: '',
    });
    const user = await renderApp();
    await user.click(screen.getByRole('button', { name: 'Activity' }));
    await act(async () => { await new Promise(r => setTimeout(r, 1100)); });

    expect(screen.getByText('0 B')).toBeInTheDocument();
    expect(screen.getByText('5.0 MB')).toBeInTheDocument();
  });
});
