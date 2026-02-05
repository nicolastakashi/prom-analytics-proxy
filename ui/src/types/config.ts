export interface PostgreSQLConfig {
  addr: string;
  database: string;
  dial_timeout: string;
  password: string;
  port: number;
  sslmode: string;
  user: string;
}

export interface SQLiteConfig {
  database_path: string;
}

export interface DatabaseConfig {
  provider: string;
  postgresql: PostgreSQLConfig;
  sqlite: SQLiteConfig;
}

export interface UpstreamConfig {
  url: string;
  include_query_stats: boolean;
}

export interface ServerConfig {
  insecure_listen_address: string;
}

export interface InsertConfig {
  batch_size: number;
  buffer_size: number;
  flush_interval: string;
  grace_period: string;
  timeout: string;
}

export interface CORSConfig {
  allowed_origins: string[];
  allowed_methods: string[];
  allowed_headers: string[];
  allow_credentials: boolean;
  max_age: number;
}

export interface TracingConfig {
  service_name?: string;
  endpoint?: string;
  insecure?: boolean;
  headers?: Record<string, string>;
  timeout?: string;
}

export interface ConfigResponse {
  upstream: UpstreamConfig;
  server: ServerConfig;
  database: DatabaseConfig;
  insert: InsertConfig;
  tracing?: TracingConfig;
  metadata_limit: number;
  series_limit: number;
  cors: CORSConfig;
}
