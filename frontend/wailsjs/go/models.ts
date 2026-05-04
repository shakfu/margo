export namespace main {
	
	export class AttachmentInput {
	    name: string;
	    mimeType: string;
	    data: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mimeType = source["mimeType"];
	        this.data = source["data"];
	    }
	}
	export class ChatMessage {
	    role: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	    }
	}
	export class ChatOptions {
	    model: string;
	    maxTokens: number;
	    temperature?: number;
	    topP?: number;
	    stopSequences: string[];
	    thinkEnabled: boolean;
	    thinkBudget: number;
	
	    static createFrom(source: any = {}) {
	        return new ChatOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.maxTokens = source["maxTokens"];
	        this.temperature = source["temperature"];
	        this.topP = source["topP"];
	        this.stopSequences = source["stopSequences"];
	        this.thinkEnabled = source["thinkEnabled"];
	        this.thinkBudget = source["thinkBudget"];
	    }
	}
	export class StreamUsage {
	    inputTokens: number;
	    outputTokens: number;
	    firstTokenMs: number;
	    totalMs: number;
	
	    static createFrom(source: any = {}) {
	        return new StreamUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.firstTokenMs = source["firstTokenMs"];
	        this.totalMs = source["totalMs"];
	    }
	}
	export class ChatResponse {
	    text: string;
	    thinking: string;
	    model: string;
	    usage: StreamUsage;
	
	    static createFrom(source: any = {}) {
	        return new ChatResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.thinking = source["thinking"];
	        this.model = source["model"];
	        this.usage = this.convertValues(source["usage"], StreamUsage);
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

