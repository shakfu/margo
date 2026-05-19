export namespace core {
	
	export class ExportStep {
	    kind: string;
	    name: string;
	    arguments?: string;
	    result?: string;
	    isError?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExportStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	        this.result = source["result"];
	        this.isError = source["isError"];
	    }
	}
	export class ExportAttachment {
	    name: string;
	    mimeType: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new ExportAttachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.mimeType = source["mimeType"];
	        this.size = source["size"];
	    }
	}
	export class ExportMessage {
	    role: string;
	    content: string;
	    thinking?: string;
	    attachments?: ExportAttachment[];
	    steps?: ExportStep[];
	
	    static createFrom(source: any = {}) {
	        return new ExportMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.thinking = source["thinking"];
	        this.attachments = this.convertValues(source["attachments"], ExportAttachment);
	        this.steps = this.convertValues(source["steps"], ExportStep);
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
	export class ChatExport {
	    title: string;
	    provider: string;
	    model: string;
	    personaName?: string;
	    agentName?: string;
	    createdAt?: string;
	    updatedAt?: string;
	    tokensIn?: number;
	    tokensOut?: number;
	    messages: ExportMessage[];
	
	    static createFrom(source: any = {}) {
	        return new ChatExport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.personaName = source["personaName"];
	        this.agentName = source["agentName"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.tokensIn = source["tokensIn"];
	        this.tokensOut = source["tokensOut"];
	        this.messages = this.convertValues(source["messages"], ExportMessage);
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
	export class MCPServerInfo {
	    name: string;
	    command: string;
	    args?: string[];
	    status: string;
	    error?: string;
	    tools?: string[];
	    stderrTail?: string[];
	
	    static createFrom(source: any = {}) {
	        return new MCPServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.tools = source["tools"];
	        this.stderrTail = source["stderrTail"];
	    }
	}

}

export namespace mcp {
	
	export class ServerSpec {
	    command: string;
	    args?: string[];
	    env?: Record<string, string>;
	    cwd?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.cwd = source["cwd"];
	    }
	}

}

