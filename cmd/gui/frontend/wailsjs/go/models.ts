export namespace main {
	
	export class FileInfoView {
	    file_id: string;
	    path: string;
	    language: string;
	    dependencies: string[];
	    file_size: number;
	    last_modified: string;
	    file_summary: string;
	
	    static createFrom(source: any = {}) {
	        return new FileInfoView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.file_id = source["file_id"];
	        this.path = source["path"];
	        this.language = source["language"];
	        this.dependencies = source["dependencies"];
	        this.file_size = source["file_size"];
	        this.last_modified = source["last_modified"];
	        this.file_summary = source["file_summary"];
	    }
	}
	export class FileNode {
	    name: string;
	    path: string;
	    file_id: string;
	    is_dir: boolean;
	    children?: FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new FileNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.file_id = source["file_id"];
	        this.is_dir = source["is_dir"];
	        this.children = this.convertValues(source["children"], FileNode);
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
	export class SnippetView {
	    snippet_id: string;
	    name: string;
	    snippet_type: string;
	    line_start: number;
	    line_end: number;
	    description: string;
	    wikilinks: string[];
	
	    static createFrom(source: any = {}) {
	        return new SnippetView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.snippet_id = source["snippet_id"];
	        this.name = source["name"];
	        this.snippet_type = source["snippet_type"];
	        this.line_start = source["line_start"];
	        this.line_end = source["line_end"];
	        this.description = source["description"];
	        this.wikilinks = source["wikilinks"];
	    }
	}
	export class WikilinkEdgeInfo {
	    snippet_id: string;
	    name: string;
	    snippet_type: string;
	    line_start: number;
	    line_end: number;
	    file_path: string;
	    file_id: string;
	
	    static createFrom(source: any = {}) {
	        return new WikilinkEdgeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.snippet_id = source["snippet_id"];
	        this.name = source["name"];
	        this.snippet_type = source["snippet_type"];
	        this.line_start = source["line_start"];
	        this.line_end = source["line_end"];
	        this.file_path = source["file_path"];
	        this.file_id = source["file_id"];
	    }
	}

}

