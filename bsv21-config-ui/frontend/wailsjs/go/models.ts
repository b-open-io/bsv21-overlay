export namespace config {
	
	export class PeerSettings {
	    sse: boolean;
	    gasp: boolean;
	    broadcast: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PeerSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sse = source["sse"];
	        this.gasp = source["gasp"];
	        this.broadcast = source["broadcast"];
	    }
	}
	export class TopicPeerConfig {
	    peers: Record<string, PeerSettings>;
	
	    static createFrom(source: any = {}) {
	        return new TopicPeerConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.peers = this.convertValues(source["peers"], PeerSettings, true);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

