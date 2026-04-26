export interface ConfigDTO {
  active_profile: string;
  upstream_host: string;
  upstream_port: number;
  username: string;
  password: string;
  verify_tls: boolean;
  custom_ca_pem: string;
  connect_timeout: number;
  idle_timeout: number;
  bind_host: string;
  bind_port: number;
}

export interface ProfileSummary {
  name: string;
}

export interface MetricsSnapshot {
  active_connections: number;
  total_requests: number;
  bytes_in: number;
  bytes_out: number;
  last_error: string;
}

export interface LogEntry {
  time: string;
  level: string;
  message: string;
  _id?: number;
}

export interface CheckResult {
  ok: boolean;
  message: string;
  latency: string;
}
