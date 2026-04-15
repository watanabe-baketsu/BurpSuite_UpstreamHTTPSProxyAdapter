export namespace adapter {
	
	export class MetricsSnapshot {
	    active_connections: number;
	    total_requests: number;
	    bytes_in: number;
	    bytes_out: number;
	    last_error: string;
	
	    static createFrom(source: any = {}) {
	        return new MetricsSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active_connections = source["active_connections"];
	        this.total_requests = source["total_requests"];
	        this.bytes_in = source["bytes_in"];
	        this.bytes_out = source["bytes_out"];
	        this.last_error = source["last_error"];
	    }
	}

}

export namespace logging {
	
	export class Entry {
	    time: string;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.message = source["message"];
	    }
	}

}

export namespace main {
	
	export class ConfigDTO {
	    upstream_host: string;
	    upstream_port: number;
	    username: string;
	    password: string;
	    verify_tls: boolean;
	    custom_ca_path: string;
	    connect_timeout: number;
	    idle_timeout: number;
	    bind_host: string;
	    bind_port: number;
	
	    static createFrom(source: any = {}) {
	        return new ConfigDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.upstream_host = source["upstream_host"];
	        this.upstream_port = source["upstream_port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.verify_tls = source["verify_tls"];
	        this.custom_ca_path = source["custom_ca_path"];
	        this.connect_timeout = source["connect_timeout"];
	        this.idle_timeout = source["idle_timeout"];
	        this.bind_host = source["bind_host"];
	        this.bind_port = source["bind_port"];
	    }
	}

}

export namespace upstream {
	
	export class CheckResult {
	    ok: boolean;
	    message: string;
	    latency: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.latency = source["latency"];
	    }
	}

}

