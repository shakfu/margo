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
	export class IndexResult {
	    path: string;
	    fileCount: number;
	    chunkCount: number;
	
	    static createFrom(source: any = {}) {
	        return new IndexResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.fileCount = source["fileCount"];
	        this.chunkCount = source["chunkCount"];
	    }
	}
	export class KnowledgeSource {
	    path: string;
	    isDir: boolean;
	    fileCount: number;
	    chunkCount: number;
	    indexedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new KnowledgeSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.isDir = source["isDir"];
	        this.fileCount = source["fileCount"];
	        this.chunkCount = source["chunkCount"];
	        this.indexedAt = source["indexedAt"];
	    }
	}
	export class StoredAttachment {
	    path: string;
	    name: string;
	    mimeType: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new StoredAttachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.mimeType = source["mimeType"];
	        this.size = source["size"];
	    }
	}
	
	export class ToolMetadata {
	    name: string;
	    description: string;
	    isReadOnly: boolean;
	    isStreamable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.isReadOnly = source["isReadOnly"];
	        this.isStreamable = source["isStreamable"];
	    }
	}

}

