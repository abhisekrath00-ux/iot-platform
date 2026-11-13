WITH target AS (
 SELECT c.oid,c.relname,c.relkind,c.relrowsecurity,c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname=ANY($1::text[])
), items AS (
 SELECT 'relation' AS kind,t.relname AS relation,'' AS name,jsonb_build_object('kind',t.relkind,'row_security',t.relrowsecurity,'force_row_security',t.relforcerowsecurity)::text AS definition FROM target t
 UNION ALL SELECT 'column',t.relname,a.attname,jsonb_build_object('type',format_type(a.atttypid,a.atttypmod),'not_null',a.attnotnull,'default',pg_get_expr(d.adbin,d.adrelid),'identity',a.attidentity,'generated',a.attgenerated)::text
 FROM target t JOIN pg_attribute a ON a.attrelid=t.oid LEFT JOIN pg_attrdef d ON d.adrelid=t.oid AND d.adnum=a.attnum WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraint',t.relname,c.conname,jsonb_build_object('type',c.contype,'definition',pg_get_constraintdef(c.oid,false),'validated',c.convalidated,'deferrable',c.condeferrable,'deferred',c.condeferred)::text FROM target t JOIN pg_constraint c ON c.conrelid=t.oid
 UNION ALL SELECT 'index',t.relname,c.relname,jsonb_build_object('definition',pg_get_indexdef(i.indexrelid,0,false),'valid',i.indisvalid,'ready',i.indisready)::text FROM target t JOIN pg_index i ON i.indrelid=t.oid JOIN pg_class c ON c.oid=i.indexrelid
 UNION ALL SELECT 'trigger',t.relname,g.tgname,pg_get_triggerdef(g.oid,false) FROM target t JOIN pg_trigger g ON g.tgrelid=t.oid WHERE NOT g.tgisinternal
 UNION ALL SELECT 'rule',t.relname,r.rulename,pg_get_ruledef(r.oid,false) FROM target t JOIN pg_rewrite r ON r.ev_class=t.oid
 UNION ALL SELECT 'policy',t.relname,p.polname,p.polname FROM target t JOIN pg_policy p ON p.polrelid=t.oid
 UNION ALL SELECT 'profile_column','device_profiles',a.attname,jsonb_build_object('type',format_type(a.atttypid,a.atttypmod),'not_null',a.attnotnull,'default',pg_get_expr(d.adbin,d.adrelid),'identity',a.attidentity,'generated',a.attgenerated)::text FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE n.nspname=current_schema() AND c.relname='device_profiles' AND a.attname='connection_defaults' AND a.attnum>0 AND NOT a.attisdropped
) SELECT kind,relation,name,definition FROM items ORDER BY kind,relation,name
