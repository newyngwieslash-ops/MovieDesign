CREATE TABLE deliveries (
 project_id TEXT NOT NULL, request_id TEXT NOT NULL, input_json TEXT NOT NULL,
 stage TEXT NOT NULL, status TEXT NOT NULL, receipt TEXT,
 origin TEXT NOT NULL, plan_json TEXT,
 PRIMARY KEY(project_id,request_id)
);
CREATE TABLE delivery_sources (
 project_id TEXT NOT NULL, request_id TEXT NOT NULL, source_key TEXT NOT NULL,
 source_path TEXT NOT NULL, archive_path TEXT NOT NULL,
 asset_id TEXT NOT NULL, resource_id TEXT NOT NULL, version_id TEXT NOT NULL,
 base_version_id TEXT, stage TEXT NOT NULL, source_json TEXT NOT NULL,
 PRIMARY KEY(project_id,request_id,source_key),
 FOREIGN KEY(project_id,request_id) REFERENCES deliveries(project_id,request_id)
);
