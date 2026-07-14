export namespace main {
	
	export class AskSubmittedAnswer {
	    questionId: string;
	    selectedOptionIds: string[];
	    customText?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskSubmittedAnswer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.selectedOptionIds = source["selectedOptionIds"];
	        this.customText = source["customText"];
	    }
	}
	export class AskSubmitRequest {
	    askId: string;
	    sessionId: string;
	    answers: AskSubmittedAnswer[];
	
	    static createFrom(source: any = {}) {
	        return new AskSubmitRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.askId = source["askId"];
	        this.sessionId = source["sessionId"];
	        this.answers = this.convertValues(source["answers"], AskSubmittedAnswer);
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
	
	export class AttachmentInput {
	    id?: string;
	    name: string;
	    type?: string;
	    size?: number;
	    kind?: string;
	    dataUrl?: string;
	    text?: string;
	    truncated?: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.size = source["size"];
	        this.kind = source["kind"];
	        this.dataUrl = source["dataUrl"];
	        this.text = source["text"];
	        this.truncated = source["truncated"];
	        this.error = source["error"];
	    }
	}
	export class BatchReadFileRequest {
	    path: string;
	    startLine?: number;
	    endLine?: number;
	    sheet?: string;
	    maxChars?: number;
	
	    static createFrom(source: any = {}) {
	        return new BatchReadFileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.sheet = source["sheet"];
	        this.maxChars = source["maxChars"];
	    }
	}
	export class BatchReadRequest {
	    path?: string;
	    paths?: string[];
	    files?: BatchReadFileRequest[];
	    startLine?: number;
	    endLine?: number;
	    sheet?: string;
	    maxChars?: number;
	
	    static createFrom(source: any = {}) {
	        return new BatchReadRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.paths = source["paths"];
	        this.files = this.convertValues(source["files"], BatchReadFileRequest);
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.sheet = source["sheet"];
	        this.maxChars = source["maxChars"];
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
	export class BatchReadResultItem {
	    path: string;
	    content: string;
	    text?: string;
	    kind?: string;
	    contentFormat?: string;
	    type?: string;
	    editable: boolean;
	    startLine: number;
	    endLine: number;
	    nextStartLine?: number;
	    md5: string;
	    size: number;
	    totalLines: number;
	    lineEnding: string;
	    truncated: boolean;
	    rangeStatus?: string;
	    emptyRange?: boolean;
	    sheets?: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new BatchReadResultItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.text = source["text"];
	        this.kind = source["kind"];
	        this.contentFormat = source["contentFormat"];
	        this.type = source["type"];
	        this.editable = source["editable"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.nextStartLine = source["nextStartLine"];
	        this.md5 = source["md5"];
	        this.size = source["size"];
	        this.totalLines = source["totalLines"];
	        this.lineEnding = source["lineEnding"];
	        this.truncated = source["truncated"];
	        this.rangeStatus = source["rangeStatus"];
	        this.emptyRange = source["emptyRange"];
	        this.sheets = source["sheets"];
	        this.error = source["error"];
	    }
	}
	export class BatchReadResult {
	    files: BatchReadResultItem[];
	
	    static createFrom(source: any = {}) {
	        return new BatchReadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = this.convertValues(source["files"], BatchReadResultItem);
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
	
	export class ChatMessageInput {
	    role: string;
	    content: string;
	    attachments?: AttachmentInput[];
	
	    static createFrom(source: any = {}) {
	        return new ChatMessageInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.attachments = this.convertValues(source["attachments"], AttachmentInput);
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
	export class ModelConfig {
	    providerName: string;
	    apiFormat: string;
	    baseUrl: string;
	    apiKey: string;
	    model: string;
	    temperature: number;
	    maxTokens: number;
	    contextWindow: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerName = source["providerName"];
	        this.apiFormat = source["apiFormat"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.maxTokens = source["maxTokens"];
	        this.contextWindow = source["contextWindow"];
	    }
	}
	export class ConfigState {
	    providerName: string;
	    apiFormat: string;
	    baseUrl: string;
	    apiKey: string;
	    model: string;
	    workspace: string;
	    temperature: number;
	    maxTokens: number;
	    contextWindow: number;
	    customPrompt: string;
	    planMode: boolean;
	    allowPrivateNetwork: boolean;
	    gitBashPath: string;
	    models?: ModelConfig[];
	    disabledSkills?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ConfigState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerName = source["providerName"];
	        this.apiFormat = source["apiFormat"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.model = source["model"];
	        this.workspace = source["workspace"];
	        this.temperature = source["temperature"];
	        this.maxTokens = source["maxTokens"];
	        this.contextWindow = source["contextWindow"];
	        this.customPrompt = source["customPrompt"];
	        this.planMode = source["planMode"];
	        this.allowPrivateNetwork = source["allowPrivateNetwork"];
	        this.gitBashPath = source["gitBashPath"];
	        this.models = this.convertValues(source["models"], ModelConfig);
	        this.disabledSkills = source["disabledSkills"];
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
	export class ChatRequest {
	    sessionId: string;
	    message: string;
	    messages: ChatMessageInput[];
	    attachments?: AttachmentInput[];
	    config: ConfigState;
	    grillMode?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChatRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.message = source["message"];
	        this.messages = this.convertValues(source["messages"], ChatMessageInput);
	        this.attachments = this.convertValues(source["attachments"], AttachmentInput);
	        this.config = this.convertValues(source["config"], ConfigState);
	        this.grillMode = source["grillMode"];
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
	export class CommandRequest {
	    command: string;
	    cwd: string;
	    timeoutSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new CommandRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	    }
	}
	export class CommandResult {
	    command: string;
	    cwd: string;
	    shell: string;
	    shellPath: string;
	    output: string;
	    exitCode: number;
	    timedOut: boolean;
	    durationMs: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.shell = source["shell"];
	        this.shellPath = source["shellPath"];
	        this.output = source["output"];
	        this.exitCode = source["exitCode"];
	        this.timedOut = source["timedOut"];
	        this.durationMs = source["durationMs"];
	        this.truncated = source["truncated"];
	    }
	}
	
	export class ContextBreakdownPart {
	    label: string;
	    tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ContextBreakdownPart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.tokens = source["tokens"];
	    }
	}
	export class ContextBreakdown {
	    total: number;
	    systemPrompt: number;
	    systemPromptParts?: ContextBreakdownPart[];
	    toolSchemas: number;
	    userMessages: number;
	    assistantMsgs: number;
	    toolResults: number;
	    reasoning: number;
	
	    static createFrom(source: any = {}) {
	        return new ContextBreakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.systemPrompt = source["systemPrompt"];
	        this.systemPromptParts = this.convertValues(source["systemPromptParts"], ContextBreakdownPart);
	        this.toolSchemas = source["toolSchemas"];
	        this.userMessages = source["userMessages"];
	        this.assistantMsgs = source["assistantMsgs"];
	        this.toolResults = source["toolResults"];
	        this.reasoning = source["reasoning"];
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
	
	export class CreateFileRequest {
	    path: string;
	    content: string;
	    overwrite: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CreateFileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.overwrite = source["overwrite"];
	    }
	}
	export class DeletePathRequest {
	    path: string;
	    recursive: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DeletePathRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.recursive = source["recursive"];
	    }
	}
	export class EditResult {
	    path: string;
	    beforeSha256: string;
	    afterSha256: string;
	    beforeMd5: string;
	    afterMd5: string;
	    beforeBytes: number;
	    afterBytes: number;
	    replacements: number;
	    addedLines: number;
	    removedLines: number;
	    lineEnding: string;
	    summary: string;
	    diff?: string;
	    firstChangedLine?: number;
	    lastChangedLine?: number;
	    warnings?: string[];
	    classification?: string;
	    changedLinesBlock?: string;
	
	    static createFrom(source: any = {}) {
	        return new EditResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.beforeSha256 = source["beforeSha256"];
	        this.afterSha256 = source["afterSha256"];
	        this.beforeMd5 = source["beforeMd5"];
	        this.afterMd5 = source["afterMd5"];
	        this.beforeBytes = source["beforeBytes"];
	        this.afterBytes = source["afterBytes"];
	        this.replacements = source["replacements"];
	        this.addedLines = source["addedLines"];
	        this.removedLines = source["removedLines"];
	        this.lineEnding = source["lineEnding"];
	        this.summary = source["summary"];
	        this.diff = source["diff"];
	        this.firstChangedLine = source["firstChangedLine"];
	        this.lastChangedLine = source["lastChangedLine"];
	        this.warnings = source["warnings"];
	        this.classification = source["classification"];
	        this.changedLinesBlock = source["changedLinesBlock"];
	    }
	}
	export class FileEntry {
	    path: string;
	    name: string;
	    dir: boolean;
	    size: number;
	    modTime: string;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.size = source["size"];
	        this.modTime = source["modTime"];
	    }
	}
	export class GitDiffFile {
	    path: string;
	    status: string;
	    diff: string;
	    added: number;
	    deleted: number;
	    truncated: boolean;
	    binary: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GitDiffFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.status = source["status"];
	        this.diff = source["diff"];
	        this.added = source["added"];
	        this.deleted = source["deleted"];
	        this.truncated = source["truncated"];
	        this.binary = source["binary"];
	        this.error = source["error"];
	    }
	}
	export class GitDiffResult {
	    isRepo: boolean;
	    branch: string;
	    files: GitDiffFile[];
	    truncated: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GitDiffResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isRepo = source["isRepo"];
	        this.branch = source["branch"];
	        this.files = this.convertValues(source["files"], GitDiffFile);
	        this.truncated = source["truncated"];
	        this.error = source["error"];
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
	export class GitStatus {
	    added: number;
	    modified: number;
	    deleted: number;
	    isRepo: boolean;
	    branch: string;
	
	    static createFrom(source: any = {}) {
	        return new GitStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.added = source["added"];
	        this.modified = source["modified"];
	        this.deleted = source["deleted"];
	        this.isRepo = source["isRepo"];
	        this.branch = source["branch"];
	    }
	}
	export class GrepMatch {
	    path: string;
	    lineNum: number;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new GrepMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.lineNum = source["lineNum"];
	        this.content = source["content"];
	    }
	}
	export class GrepRequest {
	    pattern: string;
	    path: string;
	    glob?: string;
	    maxDepth?: number;
	    maxFiles?: number;
	    maxMatches?: number;
	    timeoutSeconds?: number;
	    includeIgnored?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GrepRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pattern = source["pattern"];
	        this.path = source["path"];
	        this.glob = source["glob"];
	        this.maxDepth = source["maxDepth"];
	        this.maxFiles = source["maxFiles"];
	        this.maxMatches = source["maxMatches"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	        this.includeIgnored = source["includeIgnored"];
	    }
	}
	export class GrepResult {
	    matches: GrepMatch[];
	    count: number;
	    occurrences: number;
	    files: number;
	    truncated: boolean;
	    samplesTruncated: boolean;
	    statsExact: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GrepResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.matches = this.convertValues(source["matches"], GrepMatch);
	        this.count = source["count"];
	        this.occurrences = source["occurrences"];
	        this.files = source["files"];
	        this.truncated = source["truncated"];
	        this.samplesTruncated = source["samplesTruncated"];
	        this.statsExact = source["statsExact"];
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
	export class ListFilesRequest {
	    path: string;
	    maxDepth: number;
	    limit: number;
	    includeHidden: boolean;
	    includeIgnored: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ListFilesRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.maxDepth = source["maxDepth"];
	        this.limit = source["limit"];
	        this.includeHidden = source["includeHidden"];
	        this.includeIgnored = source["includeIgnored"];
	    }
	}
	
	export class ReadFileRequest {
	    path: string;
	    startLine: number;
	    endLine?: number;
	    lineCount: number;
	    contextBefore?: number;
	    contextAfter?: number;
	    sheet?: string;
	    maxChars?: number;
	
	    static createFrom(source: any = {}) {
	        return new ReadFileRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.lineCount = source["lineCount"];
	        this.contextBefore = source["contextBefore"];
	        this.contextAfter = source["contextAfter"];
	        this.sheet = source["sheet"];
	        this.maxChars = source["maxChars"];
	    }
	}
	export class ReadFileResult {
	    path: string;
	    content: string;
	    text?: string;
	    kind?: string;
	    contentFormat?: string;
	    type?: string;
	    editable: boolean;
	    startLine: number;
	    endLine: number;
	    nextStartLine?: number;
	    totalLines: number;
	    sha256: string;
	    md5: string;
	    size: number;
	    lineEnding: string;
	    truncated: boolean;
	    rangeStatus?: string;
	    emptyRange?: boolean;
	    sheets?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ReadFileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.text = source["text"];
	        this.kind = source["kind"];
	        this.contentFormat = source["contentFormat"];
	        this.type = source["type"];
	        this.editable = source["editable"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.nextStartLine = source["nextStartLine"];
	        this.totalLines = source["totalLines"];
	        this.sha256 = source["sha256"];
	        this.md5 = source["md5"];
	        this.size = source["size"];
	        this.lineEnding = source["lineEnding"];
	        this.truncated = source["truncated"];
	        this.rangeStatus = source["rangeStatus"];
	        this.emptyRange = source["emptyRange"];
	        this.sheets = source["sheets"];
	    }
	}
	export class ReplaceExactRequest {
	    path: string;
	    expectedSha256: string;
	    oldString: string;
	    newString: string;
	    replaceAll: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ReplaceExactRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.expectedSha256 = source["expectedSha256"];
	        this.oldString = source["oldString"];
	        this.newString = source["newString"];
	        this.replaceAll = source["replaceAll"];
	    }
	}
	export class ReplaceLinesRequest {
	    path: string;
	    expectedSha256: string;
	    startLine: number;
	    endLine: number;
	    newText: string;
	
	    static createFrom(source: any = {}) {
	        return new ReplaceLinesRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.expectedSha256 = source["expectedSha256"];
	        this.startLine = source["startLine"];
	        this.endLine = source["endLine"];
	        this.newText = source["newText"];
	    }
	}
	export class ScheduledTaskSchedule {
	    type: string;
	    at?: string;
	    every?: string;
	    cron?: string;
	    timezone?: string;
	
	    static createFrom(source: any = {}) {
	        return new ScheduledTaskSchedule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.at = source["at"];
	        this.every = source["every"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	    }
	}
	export class ScheduledTask {
	    id: string;
	    name: string;
	    instruction: string;
	    workspace: string;
	    schedule: ScheduledTaskSchedule;
	    permissionMode: string;
	    maxSteps: number;
	    timeoutSeconds: number;
	    createdAt: number;
	    updatedAt: number;
	    nextRunAt?: number;
	    lastRunAt?: number;
	    lastStatus: string;
	    lastSummary?: string;
	    lastError?: string;
	    runCount: number;
	    consecutiveFailures: number;
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScheduledTask(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.instruction = source["instruction"];
	        this.workspace = source["workspace"];
	        this.schedule = this.convertValues(source["schedule"], ScheduledTaskSchedule);
	        this.permissionMode = source["permissionMode"];
	        this.maxSteps = source["maxSteps"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.nextRunAt = source["nextRunAt"];
	        this.lastRunAt = source["lastRunAt"];
	        this.lastStatus = source["lastStatus"];
	        this.lastSummary = source["lastSummary"];
	        this.lastError = source["lastError"];
	        this.runCount = source["runCount"];
	        this.consecutiveFailures = source["consecutiveFailures"];
	        this.running = source["running"];
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
	
	export class ServiceInfo {
	    id: string;
	    name?: string;
	    command: string;
	    cwd: string;
	    pid: number;
	    port?: number;
	    status: string;
	    startedAt: number;
	    stoppedAt?: number;
	    exitCode?: number;
	    outputTail?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ServiceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.pid = source["pid"];
	        this.port = source["port"];
	        this.status = source["status"];
	        this.startedAt = source["startedAt"];
	        this.stoppedAt = source["stoppedAt"];
	        this.exitCode = source["exitCode"];
	        this.outputTail = source["outputTail"];
	        this.error = source["error"];
	    }
	}
	export class ServiceListResult {
	    services: ServiceInfo[];
	
	    static createFrom(source: any = {}) {
	        return new ServiceListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.services = this.convertValues(source["services"], ServiceInfo);
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
	export class SkillDefinition {
	    name: string;
	    description: string;
	    source: string;
	    path: string;
	    dir: string;
	    type: string;
	    whenToUse: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillDefinition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.source = source["source"];
	        this.path = source["path"];
	        this.dir = source["dir"];
	        this.type = source["type"];
	        this.whenToUse = source["whenToUse"];
	    }
	}
	export class StartServiceRequest {
	    name?: string;
	    command: string;
	    cwd?: string;
	    port?: number;
	    readyPattern?: string;
	    timeoutSeconds?: number;
	
	    static createFrom(source: any = {}) {
	        return new StartServiceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.port = source["port"];
	        this.readyPattern = source["readyPattern"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	    }
	}
	export class StopServiceRequest {
	    id: string;
	
	    static createFrom(source: any = {}) {
	        return new StopServiceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	    }
	}
	export class SubToolEvent {
	    toolCallId: string;
	    name: string;
	    args: string;
	    status: string;
	    summary?: string;
	    durationMs?: number;
	
	    static createFrom(source: any = {}) {
	        return new SubToolEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.toolCallId = source["toolCallId"];
	        this.name = source["name"];
	        this.args = source["args"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.durationMs = source["durationMs"];
	    }
	}
	export class SubagentRun {
	    id: string;
	    sessionId?: string;
	    description: string;
	    profile: string;
	    status: string;
	    steps: number;
	    maxSteps: number;
	    summary?: string;
	    filesRead?: string[];
	    filesEdited?: string[];
	    error?: string;
	    toolCalls?: SubToolEvent[];
	    startTime: number;
	
	    static createFrom(source: any = {}) {
	        return new SubagentRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.description = source["description"];
	        this.profile = source["profile"];
	        this.status = source["status"];
	        this.steps = source["steps"];
	        this.maxSteps = source["maxSteps"];
	        this.summary = source["summary"];
	        this.filesRead = source["filesRead"];
	        this.filesEdited = source["filesEdited"];
	        this.error = source["error"];
	        this.toolCalls = this.convertValues(source["toolCalls"], SubToolEvent);
	        this.startTime = source["startTime"];
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
	export class TodoEntry {
	    title: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new TodoEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.status = source["status"];
	    }
	}
	export class ToolDefinitionSummary {
	    name: string;
	    description: string;
	    source: string;
	    server?: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolDefinitionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.source = source["source"];
	        this.server = source["server"];
	    }
	}
	export class WorkspaceTokenUsage {
	    inputTokens: number;
	    outputTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceTokenUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	    }
	}

}

