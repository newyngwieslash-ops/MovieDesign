CREATE TABLE projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE composition_types(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,label TEXT NOT NULL,UNIQUE(project_id,label));
CREATE TABLE asset_projects(asset_id TEXT PRIMARY KEY REFERENCES assets(id),project_id TEXT NOT NULL REFERENCES projects(id));
CREATE TABLE write_requests(project_id TEXT NOT NULL,operation TEXT NOT NULL,request_id TEXT NOT NULL,input_json TEXT NOT NULL,state TEXT NOT NULL,response_json TEXT,PRIMARY KEY(project_id,operation,request_id));
CREATE TABLE file_operations(project_id TEXT NOT NULL,request_id TEXT NOT NULL,operation TEXT NOT NULL,input_json TEXT NOT NULL,stage TEXT NOT NULL,source TEXT,target TEXT,receipt TEXT,PRIMARY KEY(project_id,request_id));
