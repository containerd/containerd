// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated from semantic convention specification. DO NOT EDIT.

package semconv // import "go.opentelemetry.io/otel/semconv/v1.17.0"

import "go.opentelemetry.io/otel/attribute"

// This document defines the shared attributes used to report a single exception associated with a span or log.
const (
	// The type of the exception (its fully-qualified class name, if applicable). The
	// dynamic type of the exception should be preferred over the static type in
	// languages that support it.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'java.net.ConnectException', 'OSError'
	ExceptionTypeKey = attribute.Key("exception.type")
	// The exception message.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Division by zero', "Can't convert 'int' object to str implicitly"
	ExceptionMessageKey = attribute.Key("exception.message")
	// A stacktrace as a string in the natural representation for the language
	// runtime. The representation is to be determined and documented by each language
	// SIG.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Exception in thread "main" java.lang.RuntimeException: Test
	// exception\\n at '
	//  'com.example.GenerateTrace.methodB(GenerateTrace.java:13)\\n at '
	//  'com.example.GenerateTrace.methodA(GenerateTrace.java:9)\\n at '
	//  'com.example.GenerateTrace.main(GenerateTrace.java:5)'
	ExceptionStacktraceKey = attribute.Key("exception.stacktrace")
)

// This document defines attributes for Events represented using Log Records.
const (
	// The name identifies the event.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'click', 'exception'
	EventNameKey = attribute.Key("event.name")
	// The domain identifies the business context for the events.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	// Note: Events across different domains may have same `event.name`, yet be
	// unrelated events.
	EventDomainKey = attribute.Key("event.domain")
)

var (
	// Events from browser apps
	EventDomainBrowser = EventDomainKey.String("browser")
	// Events from mobile apps
	EventDomainDevice = EventDomainKey.String("device")
	// Events from Kubernetes
	EventDomainK8S = EventDomainKey.String("k8s")
)

// Span attributes used by AWS Lambda (in addition to general `faas` attributes).
const (
	// The full invoked ARN as provided on the `Context` passed to the function
	// (`Lambda-Runtime-Invoked-Function-ARN` header on the `/runtime/invocation/next`
	// applicable).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'arn:aws:lambda:us-east-1:123456:function:myfunction:myalias'
	// Note: This may be different from `faas.id` if an alias is involved.
	AWSLambdaInvokedARNKey = attribute.Key("aws.lambda.invoked_arn")
)

// This document defines attributes for CloudEvents. CloudEvents is a specification on how to define event data in a standard way. These attributes can be attached to spans when performing operations with CloudEvents, regardless of the protocol being used.
const (
	// The [event_id](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec
	// .md#id) uniquely identifies the event.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: '123e4567-e89b-12d3-a456-426614174000', '0001'
	CloudeventsEventIDKey = attribute.Key("cloudevents.event_id")
	// The [source](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.m
	// d#source-1) identifies the context in which an event happened.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'https://github.com/cloudevents', '/cloudevents/spec/pull/123', 'my-
	// service'
	CloudeventsEventSourceKey = attribute.Key("cloudevents.event_source")
	// The [version of the CloudEvents specification](https://github.com/cloudevents/s
	// pec/blob/v1.0.2/cloudevents/spec.md#specversion) which the event uses.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '1.0'
	CloudeventsEventSpecVersionKey = attribute.Key("cloudevents.event_spec_version")
	// The [event_type](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/sp
	// ec.md#type) contains a value describing the type of event related to the
	// originating occurrence.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'com.github.pull_request.opened', 'com.example.object.deleted.v2'
	CloudeventsEventTypeKey = attribute.Key("cloudevents.event_type")
	// The [subject](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.
	// md#subject) of the event in the context of the event producer (identified by
	// source).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'mynewfile.jpg'
	CloudeventsEventSubjectKey = attribute.Key("cloudevents.event_subject")
)

// This document defines semantic conventions for the OpenTracing Shim
const (
	// Parent-child Reference type
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Note: The causal relationship between a child Span and a parent Span.
	OpentracingRefTypeKey = attribute.Key("opentracing.ref_type")
)

var (
	// The parent Span depends on the child Span in some capacity
	OpentracingRefTypeChildOf = OpentracingRefTypeKey.String("child_of")
	// The parent Span does not depend in any way on the result of the child Span
	OpentracingRefTypeFollowsFrom = OpentracingRefTypeKey.String("follows_from")
)

// This document defines the attributes used to perform database client calls.
const (
	// An identifier for the database management system (DBMS) product being used. See
	// below for a list of well-known identifiers.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	DBSystemKey = attribute.Key("db.system")
	// The connection string used to connect to the database. It is recommended to
	// remove embedded credentials.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Server=(localdb)\\v11.0;Integrated Security=true;'
	DBConnectionStringKey = attribute.Key("db.connection_string")
	// Username for accessing the database.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'readonly_user', 'reporting_user'
	DBUserKey = attribute.Key("db.user")
	// The fully-qualified class name of the [Java Database Connectivity
	// (JDBC)](https://docs.oracle.com/javase/8/docs/technotes/guides/jdbc/) driver
	// used to connect.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'org.postgresql.Driver',
	// 'com.microsoft.sqlserver.jdbc.SQLServerDriver'
	DBJDBCDriverClassnameKey = attribute.Key("db.jdbc.driver_classname")
	// This attribute is used to report the name of the database being accessed. For
	// commands that switch the database, this should be set to the target database
	// (even if the command fails).
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If applicable.)
	// Stability: stable
	// Examples: 'customers', 'main'
	// Note: In some SQL databases, the database name to be used is called "schema
	// name". In case there are multiple layers that could be considered for database
	// name (e.g. Oracle instance name and schema name), the database name to be used
	// is the more specific layer (e.g. Oracle schema name).
	DBNameKey = attribute.Key("db.name")
	// The database statement being executed.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If applicable and not explicitly
	// disabled via instrumentation configuration.)
	// Stability: stable
	// Examples: 'SELECT * FROM wuser_table', 'SET mykey "WuValue"'
	// Note: The value may be sanitized to exclude sensitive information.
	DBStatementKey = attribute.Key("db.statement")
	// The name of the operation being executed, e.g. the [MongoDB command
	// name](https://docs.mongodb.com/manual/reference/command/#database-operations)
	// such as `findAndModify`, or the SQL keyword.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If `db.statement` is not applicable.)
	// Stability: stable
	// Examples: 'findAndModify', 'HMSET', 'SELECT'
	// Note: When setting this to an SQL keyword, it is not recommended to attempt any
	// client-side parsing of `db.statement` just to get this property, but it should
	// be set if the operation name is provided by the library being instrumented. If
	// the SQL statement has an ambiguous operation, or performs more than one
	// operation, this value may be omitted.
	DBOperationKey = attribute.Key("db.operation")
)

var (
	// Some other SQL database. Fallback only. See notes
	DBSystemOtherSQL = DBSystemKey.String("other_sql")
	// Microsoft SQL Server
	DBSystemMSSQL = DBSystemKey.String("mssql")
	// MySQL
	DBSystemMySQL = DBSystemKey.String("mysql")
	// Oracle Database
	DBSystemOracle = DBSystemKey.String("oracle")
	// IBM DB2
	DBSystemDB2 = DBSystemKey.String("db2")
	// PostgreSQL
	DBSystemPostgreSQL = DBSystemKey.String("postgresql")
	// Amazon Redshift
	DBSystemRedshift = DBSystemKey.String("redshift")
	// Apache Hive
	DBSystemHive = DBSystemKey.String("hive")
	// Cloudscape
	DBSystemCloudscape = DBSystemKey.String("cloudscape")
	// HyperSQL DataBase
	DBSystemHSQLDB = DBSystemKey.String("hsqldb")
	// Progress Database
	DBSystemProgress = DBSystemKey.String("progress")
	// SAP MaxDB
	DBSystemMaxDB = DBSystemKey.String("maxdb")
	// SAP HANA
	DBSystemHanaDB = DBSystemKey.String("hanadb")
	// Ingres
	DBSystemIngres = DBSystemKey.String("ingres")
	// FirstSQL
	DBSystemFirstSQL = DBSystemKey.String("firstsql")
	// EnterpriseDB
	DBSystemEDB = DBSystemKey.String("edb")
	// InterSystems Caché
	DBSystemCache = DBSystemKey.String("cache")
	// Adabas (Adaptable Database System)
	DBSystemAdabas = DBSystemKey.String("adabas")
	// Firebird
	DBSystemFirebird = DBSystemKey.String("firebird")
	// Apache Derby
	DBSystemDerby = DBSystemKey.String("derby")
	// FileMaker
	DBSystemFilemaker = DBSystemKey.String("filemaker")
	// Informix
	DBSystemInformix = DBSystemKey.String("informix")
	// InstantDB
	DBSystemInstantDB = DBSystemKey.String("instantdb")
	// InterBase
	DBSystemInterbase = DBSystemKey.String("interbase")
	// MariaDB
	DBSystemMariaDB = DBSystemKey.String("mariadb")
	// Netezza
	DBSystemNetezza = DBSystemKey.String("netezza")
	// Pervasive PSQL
	DBSystemPervasive = DBSystemKey.String("pervasive")
	// PointBase
	DBSystemPointbase = DBSystemKey.String("pointbase")
	// SQLite
	DBSystemSqlite = DBSystemKey.String("sqlite")
	// Sybase
	DBSystemSybase = DBSystemKey.String("sybase")
	// Teradata
	DBSystemTeradata = DBSystemKey.String("teradata")
	// Vertica
	DBSystemVertica = DBSystemKey.String("vertica")
	// H2
	DBSystemH2 = DBSystemKey.String("h2")
	// ColdFusion IMQ
	DBSystemColdfusion = DBSystemKey.String("coldfusion")
	// Apache Cassandra
	DBSystemCassandra = DBSystemKey.String("cassandra")
	// Apache HBase
	DBSystemHBase = DBSystemKey.String("hbase")
	// MongoDB
	DBSystemMongoDB = DBSystemKey.String("mongodb")
	// Redis
	DBSystemRedis = DBSystemKey.String("redis")
	// Couchbase
	DBSystemCouchbase = DBSystemKey.String("couchbase")
	// CouchDB
	DBSystemCouchDB = DBSystemKey.String("couchdb")
	// Microsoft Azure Cosmos DB
	DBSystemCosmosDB = DBSystemKey.String("cosmosdb")
	// Amazon DynamoDB
	DBSystemDynamoDB = DBSystemKey.String("dynamodb")
	// Neo4j
	DBSystemNeo4j = DBSystemKey.String("neo4j")
	// Apache Geode
	DBSystemGeode = DBSystemKey.String("geode")
	// Elasticsearch
	DBSystemElasticsearch = DBSystemKey.String("elasticsearch")
	// Memcached
	DBSystemMemcached = DBSystemKey.String("memcached")
	// CockroachDB
	DBSystemCockroachdb = DBSystemKey.String("cockroachdb")
	// OpenSearch
	DBSystemOpensearch = DBSystemKey.String("opensearch")
	// ClickHouse
	DBSystemClickhouse = DBSystemKey.String("clickhouse")
)

// Connection-level attributes for Microsoft SQL Server
const (
	// The Microsoft SQL Server [instance name](https://docs.microsoft.com/en-
	// us/sql/connect/jdbc/building-the-connection-url?view=sql-server-ver15)
	// connecting to. This name is used to determine the port of a named instance.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'MSSQLSERVER'
	// Note: If setting a `db.mssql.instance_name`, `net.peer.port` is no longer
	// required (but still recommended if non-standard).
	DBMSSQLInstanceNameKey = attribute.Key("db.mssql.instance_name")
)

// Call-level attributes for Cassandra
const (
	// The fetch size used for paging, i.e. how many rows will be returned at once.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 5000
	DBCassandraPageSizeKey = attribute.Key("db.cassandra.page_size")
	// The consistency level of the query. Based on consistency values from
	// [CQL](https://docs.datastax.com/en/cassandra-
	// oss/3.0/cassandra/dml/dmlConfigConsistency.html).
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	DBCassandraConsistencyLevelKey = attribute.Key("db.cassandra.consistency_level")
	// The name of the primary table that the operation is acting upon, including the
	// keyspace name (if applicable).
	//
	// Type: string
	// RequirementLevel: Recommended
	// Stability: stable
	// Examples: 'mytable'
	// Note: This mirrors the db.sql.table attribute but references cassandra rather
	// than sql. It is not recommended to attempt any client-side parsing of
	// `db.statement` just to get this property, but it should be set if it is
	// provided by the library being instrumented. If the operation is acting upon an
	// anonymous table, or more than one table, this value MUST NOT be set.
	DBCassandraTableKey = attribute.Key("db.cassandra.table")
	// Whether or not the query is idempotent.
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	DBCassandraIdempotenceKey = attribute.Key("db.cassandra.idempotence")
	// The number of times a query was speculatively executed. Not set or `0` if the
	// query was not executed speculatively.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 0, 2
	DBCassandraSpeculativeExecutionCountKey = attribute.Key("db.cassandra.speculative_execution_count")
	// The ID of the coordinating node for a query.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'be13faa2-8574-4d71-926d-27f16cf8a7af'
	DBCassandraCoordinatorIDKey = attribute.Key("db.cassandra.coordinator.id")
	// The data center of the coordinating node for a query.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'us-west-2'
	DBCassandraCoordinatorDCKey = attribute.Key("db.cassandra.coordinator.dc")
)

var (
	// all
	DBCassandraConsistencyLevelAll = DBCassandraConsistencyLevelKey.String("all")
	// each_quorum
	DBCassandraConsistencyLevelEachQuorum = DBCassandraConsistencyLevelKey.String("each_quorum")
	// quorum
	DBCassandraConsistencyLevelQuorum = DBCassandraConsistencyLevelKey.String("quorum")
	// local_quorum
	DBCassandraConsistencyLevelLocalQuorum = DBCassandraConsistencyLevelKey.String("local_quorum")
	// one
	DBCassandraConsistencyLevelOne = DBCassandraConsistencyLevelKey.String("one")
	// two
	DBCassandraConsistencyLevelTwo = DBCassandraConsistencyLevelKey.String("two")
	// three
	DBCassandraConsistencyLevelThree = DBCassandraConsistencyLevelKey.String("three")
	// local_one
	DBCassandraConsistencyLevelLocalOne = DBCassandraConsistencyLevelKey.String("local_one")
	// any
	DBCassandraConsistencyLevelAny = DBCassandraConsistencyLevelKey.String("any")
	// serial
	DBCassandraConsistencyLevelSerial = DBCassandraConsistencyLevelKey.String("serial")
	// local_serial
	DBCassandraConsistencyLevelLocalSerial = DBCassandraConsistencyLevelKey.String("local_serial")
)

// Call-level attributes for Redis
const (
	// The index of the database being accessed as used in the [`SELECT`
	// command](https://redis.io/commands/select), provided as an integer. To be used
	// instead of the generic `db.name` attribute.
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If other than the default database
	// (`0`).)
	// Stability: stable
	// Examples: 0, 1, 15
	DBRedisDBIndexKey = attribute.Key("db.redis.database_index")
)

// Call-level attributes for MongoDB
const (
	// The collection being accessed within the database stated in `db.name`.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'customers', 'products'
	DBMongoDBCollectionKey = attribute.Key("db.mongodb.collection")
)

// Call-level attributes for SQL databases
const (
	// The name of the primary table that the operation is acting upon, including the
	// database name (if applicable).
	//
	// Type: string
	// RequirementLevel: Recommended
	// Stability: stable
	// Examples: 'public.users', 'customers'
	// Note: It is not recommended to attempt any client-side parsing of
	// `db.statement` just to get this property, but it should be set if it is
	// provided by the library being instrumented. If the operation is acting upon an
	// anonymous table, or more than one table, this value MUST NOT be set.
	DBSQLTableKey = attribute.Key("db.sql.table")
)

// Span attributes used by non-OTLP exporters to represent OpenTelemetry Span's concepts.
const (
	// Name of the code, either "OK" or "ERROR". MUST NOT be set if the status code is
	// UNSET.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	OtelStatusCodeKey = attribute.Key("otel.status_code")
	// Description of the Status if it has a value, otherwise not set.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'resource not found'
	OtelStatusDescriptionKey = attribute.Key("otel.status_description")
)

var (
	// The operation has been validated by an Application developer or Operator to have completed successfully
	OtelStatusCodeOk = OtelStatusCodeKey.String("OK")
	// The operation contains an error
	OtelStatusCodeError = OtelStatusCodeKey.String("ERROR")
)

// This semantic convention describes an instance of a function that runs without provisioning or managing of servers (also known as serverless functions or Function as a Service (FaaS)) with spans.
const (
	// Type of the trigger which caused this function execution.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Note: For the server/consumer span on the incoming side,
	// `faas.trigger` MUST be set.

	// Clients invoking FaaS instances usually cannot set `faas.trigger`,
	// since they would typically need to look in the payload to determine
	// the event type. If clients set it, it should be the same as the
	// trigger that corresponding incoming would have (i.e., this has
	// nothing to do with the underlying transport used to make the API
	// call to invoke the lambda, which is often HTTP).
	FaaSTriggerKey = attribute.Key("faas.trigger")
	// The execution ID of the current function execution.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'af9d5aa4-a685-4c5f-a22b-444f80b3cc28'
	FaaSExecutionKey = attribute.Key("faas.execution")
)

var (
	// A response to some data source operation such as a database or filesystem read/write
	FaaSTriggerDatasource = FaaSTriggerKey.String("datasource")
	// To provide an answer to an inbound HTTP request
	FaaSTriggerHTTP = FaaSTriggerKey.String("http")
	// A function is set to be executed when messages are sent to a messaging system
	FaaSTriggerPubsub = FaaSTriggerKey.String("pubsub")
	// A function is scheduled to be executed regularly
	FaaSTriggerTimer = FaaSTriggerKey.String("timer")
	// If none of the others apply
	FaaSTriggerOther = FaaSTriggerKey.String("other")
)

// Semantic Convention for FaaS triggered as a response to some data source operation such as a database or filesystem read/write.
const (
	// The name of the source on which the triggering operation was performed. For
	// example, in Cloud Storage or S3 corresponds to the bucket name, and in Cosmos
	// DB to the database name.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'myBucketName', 'myDBName'
	FaaSDocumentCollectionKey = attribute.Key("faas.document.collection")
	// Describes the type of the operation that was performed on the data.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	FaaSDocumentOperationKey = attribute.Key("faas.document.operation")
	// A string containing the time when the data was accessed in the [ISO
	// 8601](https://www.iso.org/iso-8601-date-and-time-format.html) format expressed
	// in [UTC](https://www.w3.org/TR/NOTE-datetime).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '2020-01-23T13:47:06Z'
	FaaSDocumentTimeKey = attribute.Key("faas.document.time")
	// The document name/table subjected to the operation. For example, in Cloud
	// Storage or S3 is the name of the file, and in Cosmos DB the table name.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'myFile.txt', 'myTableName'
	FaaSDocumentNameKey = attribute.Key("faas.document.name")
)

var (
	// When a new object is created
	FaaSDocumentOperationInsert = FaaSDocumentOperationKey.String("insert")
	// When an object is modified
	FaaSDocumentOperationEdit = FaaSDocumentOperationKey.String("edit")
	// When an object is deleted
	FaaSDocumentOperationDelete = FaaSDocumentOperationKey.String("delete")
)

// Semantic Convention for FaaS scheduled to be executed regularly.
const (
	// A string containing the function invocation time in the [ISO
	// 8601](https://www.iso.org/iso-8601-date-and-time-format.html) format expressed
	// in [UTC](https://www.w3.org/TR/NOTE-datetime).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '2020-01-23T13:47:06Z'
	FaaSTimeKey = attribute.Key("faas.time")
	// A string containing the schedule period as [Cron Expression](https://docs.oracl
	// e.com/cd/E12058_01/doc/doc.1014/e12030/cron_expressions.htm).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '0/5 * * * ? *'
	FaaSCronKey = attribute.Key("faas.cron")
)

// Contains additional attributes for incoming FaaS spans.
const (
	// A boolean that is true if the serverless function is executed for the first
	// time (aka cold-start).
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	FaaSColdstartKey = attribute.Key("faas.coldstart")
)

// Contains additional attributes for outgoing FaaS spans.
const (
	// The name of the invoked function.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'my-function'
	// Note: SHOULD be equal to the `faas.name` resource attribute of the invoked
	// function.
	FaaSInvokedNameKey = attribute.Key("faas.invoked_name")
	// The cloud provider of the invoked function.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	// Note: SHOULD be equal to the `cloud.provider` resource attribute of the invoked
	// function.
	FaaSInvokedProviderKey = attribute.Key("faas.invoked_provider")
	// The cloud region of the invoked function.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (For some cloud providers, like AWS or
	// GCP, the region in which a function is hosted is essential to uniquely identify
	// the function and also part of its endpoint. Since it's part of the endpoint
	// being called, the region is always known to clients. In these cases,
	// `faas.invoked_region` MUST be set accordingly. If the region is unknown to the
	// client or not required for identifying the invoked function, setting
	// `faas.invoked_region` is optional.)
	// Stability: stable
	// Examples: 'eu-central-1'
	// Note: SHOULD be equal to the `cloud.region` resource attribute of the invoked
	// function.
	FaaSInvokedRegionKey = attribute.Key("faas.invoked_region")
)

var (
	// Alibaba Cloud
	FaaSInvokedProviderAlibabaCloud = FaaSInvokedProviderKey.String("alibaba_cloud")
	// Amazon Web Services
	FaaSInvokedProviderAWS = FaaSInvokedProviderKey.String("aws")
	// Microsoft Azure
	FaaSInvokedProviderAzure = FaaSInvokedProviderKey.String("azure")
	// Google Cloud Platform
	FaaSInvokedProviderGCP = FaaSInvokedProviderKey.String("gcp")
	// Tencent Cloud
	FaaSInvokedProviderTencentCloud = FaaSInvokedProviderKey.String("tencent_cloud")
)

// These attributes may be used for any network related operation.
const (
	// Transport protocol used. See note below.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	NetTransportKey = attribute.Key("net.transport")
	// Application layer protocol used. The value SHOULD be normalized to lowercase.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'amqp', 'http', 'mqtt'
	NetAppProtocolNameKey = attribute.Key("net.app.protocol.name")
	// Version of the application layer protocol used. See note below.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '3.1.1'
	// Note: `net.app.protocol.version` refers to the version of the protocol used and
	// might be different from the protocol client's version. If the HTTP client used
	// has a version of `0.27.2`, but sends HTTP version `1.1`, this attribute should
	// be set to `1.1`.
	NetAppProtocolVersionKey = attribute.Key("net.app.protocol.version")
	// Remote socket peer name.
	//
	// Type: string
	// RequirementLevel: Recommended (If available and different from `net.peer.name`
	// and if `net.sock.peer.addr` is set.)
	// Stability: stable
	// Examples: 'proxy.example.com'
	NetSockPeerNameKey = attribute.Key("net.sock.peer.name")
	// Remote socket peer address: IPv4 or IPv6 for internet protocols, path for local
	// communication, [etc](https://man7.org/linux/man-
	// pages/man7/address_families.7.html).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '127.0.0.1', '/tmp/mysql.sock'
	NetSockPeerAddrKey = attribute.Key("net.sock.peer.addr")
	// Remote socket peer port.
	//
	// Type: int
	// RequirementLevel: Recommended (If defined for the address family and if
	// different than `net.peer.port` and if `net.sock.peer.addr` is set.)
	// Stability: stable
	// Examples: 16456
	NetSockPeerPortKey = attribute.Key("net.sock.peer.port")
	// Protocol [address family](https://man7.org/linux/man-
	// pages/man7/address_families.7.html) which is used for communication.
	//
	// Type: Enum
	// RequirementLevel: ConditionallyRequired (If different than `inet` and if any of
	// `net.sock.peer.addr` or `net.sock.host.addr` are set. Consumers of telemetry
	// SHOULD accept both IPv4 and IPv6 formats for the address in
	// `net.sock.peer.addr` if `net.sock.family` is not set. This is to support
	// instrumentations that follow previous versions of this document.)
	// Stability: stable
	// Examples: 'inet6', 'bluetooth'
	NetSockFamilyKey = attribute.Key("net.sock.family")
	// Logical remote hostname, see note below.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'example.com'
	// Note: `net.peer.name` SHOULD NOT be set if capturing it would require an extra
	// DNS lookup.
	NetPeerNameKey = attribute.Key("net.peer.name")
	// Logical remote port number
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 80, 8080, 443
	NetPeerPortKey = attribute.Key("net.peer.port")
	// Logical local hostname or similar, see note below.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'localhost'
	NetHostNameKey = attribute.Key("net.host.name")
	// Logical local port number, preferably the one that the peer used to connect
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 8080
	NetHostPortKey = attribute.Key("net.host.port")
	// Local socket address. Useful in case of a multi-IP host.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '192.168.0.1'
	NetSockHostAddrKey = attribute.Key("net.sock.host.addr")
	// Local socket port number.
	//
	// Type: int
	// RequirementLevel: Recommended (If defined for the address family and if
	// different than `net.host.port` and if `net.sock.host.addr` is set.)
	// Stability: stable
	// Examples: 35555
	NetSockHostPortKey = attribute.Key("net.sock.host.port")
	// The internet connection type currently being used by the host.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'wifi'
	NetHostConnectionTypeKey = attribute.Key("net.host.connection.type")
	// This describes more details regarding the connection.type. It may be the type
	// of cell technology connection, but it could be used for describing details
	// about a wifi connection.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'LTE'
	NetHostConnectionSubtypeKey = attribute.Key("net.host.connection.subtype")
	// The name of the mobile carrier.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'sprint'
	NetHostCarrierNameKey = attribute.Key("net.host.carrier.name")
	// The mobile carrier country code.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '310'
	NetHostCarrierMccKey = attribute.Key("net.host.carrier.mcc")
	// The mobile carrier network code.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '001'
	NetHostCarrierMncKey = attribute.Key("net.host.carrier.mnc")
	// The ISO 3166-1 alpha-2 2-character country code associated with the mobile
	// carrier network.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'DE'
	NetHostCarrierIccKey = attribute.Key("net.host.carrier.icc")
)

var (
	// ip_tcp
	NetTransportTCP = NetTransportKey.String("ip_tcp")
	// ip_udp
	NetTransportUDP = NetTransportKey.String("ip_udp")
	// Named or anonymous pipe. See note below
	NetTransportPipe = NetTransportKey.String("pipe")
	// In-process communication
	NetTransportInProc = NetTransportKey.String("inproc")
	// Something else (non IP-based)
	NetTransportOther = NetTransportKey.String("other")
)

var (
	// IPv4 address
	NetSockFamilyInet = NetSockFamilyKey.String("inet")
	// IPv6 address
	NetSockFamilyInet6 = NetSockFamilyKey.String("inet6")
	// Unix domain socket path
	NetSockFamilyUnix = NetSockFamilyKey.String("unix")
)

var (
	// wifi
	NetHostConnectionTypeWifi = NetHostConnectionTypeKey.String("wifi")
	// wired
	NetHostConnectionTypeWired = NetHostConnectionTypeKey.String("wired")
	// cell
	NetHostConnectionTypeCell = NetHostConnectionTypeKey.String("cell")
	// unavailable
	NetHostConnectionTypeUnavailable = NetHostConnectionTypeKey.String("unavailable")
	// unknown
	NetHostConnectionTypeUnknown = NetHostConnectionTypeKey.String("unknown")
)

var (
	// GPRS
	NetHostConnectionSubtypeGprs = NetHostConnectionSubtypeKey.String("gprs")
	// EDGE
	NetHostConnectionSubtypeEdge = NetHostConnectionSubtypeKey.String("edge")
	// UMTS
	NetHostConnectionSubtypeUmts = NetHostConnectionSubtypeKey.String("umts")
	// CDMA
	NetHostConnectionSubtypeCdma = NetHostConnectionSubtypeKey.String("cdma")
	// EVDO Rel. 0
	NetHostConnectionSubtypeEvdo0 = NetHostConnectionSubtypeKey.String("evdo_0")
	// EVDO Rev. A
	NetHostConnectionSubtypeEvdoA = NetHostConnectionSubtypeKey.String("evdo_a")
	// CDMA2000 1XRTT
	NetHostConnectionSubtypeCdma20001xrtt = NetHostConnectionSubtypeKey.String("cdma2000_1xrtt")
	// HSDPA
	NetHostConnectionSubtypeHsdpa = NetHostConnectionSubtypeKey.String("hsdpa")
	// HSUPA
	NetHostConnectionSubtypeHsupa = NetHostConnectionSubtypeKey.String("hsupa")
	// HSPA
	NetHostConnectionSubtypeHspa = NetHostConnectionSubtypeKey.String("hspa")
	// IDEN
	NetHostConnectionSubtypeIden = NetHostConnectionSubtypeKey.String("iden")
	// EVDO Rev. B
	NetHostConnectionSubtypeEvdoB = NetHostConnectionSubtypeKey.String("evdo_b")
	// LTE
	NetHostConnectionSubtypeLte = NetHostConnectionSubtypeKey.String("lte")
	// EHRPD
	NetHostConnectionSubtypeEhrpd = NetHostConnectionSubtypeKey.String("ehrpd")
	// HSPAP
	NetHostConnectionSubtypeHspap = NetHostConnectionSubtypeKey.String("hspap")
	// GSM
	NetHostConnectionSubtypeGsm = NetHostConnectionSubtypeKey.String("gsm")
	// TD-SCDMA
	NetHostConnectionSubtypeTdScdma = NetHostConnectionSubtypeKey.String("td_scdma")
	// IWLAN
	NetHostConnectionSubtypeIwlan = NetHostConnectionSubtypeKey.String("iwlan")
	// 5G NR (New Radio)
	NetHostConnectionSubtypeNr = NetHostConnectionSubtypeKey.String("nr")
	// 5G NRNSA (New Radio Non-Standalone)
	NetHostConnectionSubtypeNrnsa = NetHostConnectionSubtypeKey.String("nrnsa")
	// LTE CA
	NetHostConnectionSubtypeLteCa = NetHostConnectionSubtypeKey.String("lte_ca")
)

// Operations that access some remote service.
const (
	// The [`service.name`](../../resource/semantic_conventions/README.md#service) of
	// the remote service. SHOULD be equal to the actual `service.name` resource
	// attribute of the remote service if any.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'AuthTokenCache'
	PeerServiceKey = attribute.Key("peer.service")
)

// These attributes may be used for any operation with an authenticated and/or authorized enduser.
const (
	// Username or client_id extracted from the access token or
	// [Authorization](https://tools.ietf.org/html/rfc7235#section-4.2) header in the
	// inbound request from outside the system.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'username'
	EnduserIDKey = attribute.Key("enduser.id")
	// Actual/assumed role the client is making the request under extracted from token
	// or application security context.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'admin'
	EnduserRoleKey = attribute.Key("enduser.role")
	// Scopes or granted authorities the client currently possesses extracted from
	// token or application security context. The value would come from the scope
	// associated with an [OAuth 2.0 Access
	// Token](https://tools.ietf.org/html/rfc6749#section-3.3) or an attribute value
	// in a [SAML 2.0 Assertion](http://docs.oasis-
	// open.org/security/saml/Post2.0/sstc-saml-tech-overview-2.0.html).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'read:message, write:files'
	EnduserScopeKey = attribute.Key("enduser.scope")
)

// These attributes may be used for any operation to store information about a thread that started a span.
const (
	// Current "managed" thread ID (as opposed to OS thread ID).
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 42
	ThreadIDKey = attribute.Key("thread.id")
	// Current thread name.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'main'
	ThreadNameKey = attribute.Key("thread.name")
)

// These attributes allow to report this unit of code and therefore to provide more context about the span.
const (
	// The method or function name, or equivalent (usually rightmost part of the code
	// unit's name).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'serveRequest'
	CodeFunctionKey = attribute.Key("code.function")
	// The "namespace" within which `code.function` is defined. Usually the qualified
	// class or module name, such that `code.namespace` + some separator +
	// `code.function` form a unique identifier for the code unit.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'com.example.MyHTTPService'
	CodeNamespaceKey = attribute.Key("code.namespace")
	// The source code file name that identifies the code unit as uniquely as possible
	// (preferably an absolute file path).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '/usr/local/MyApplication/content_root/app/index.php'
	CodeFilepathKey = attribute.Key("code.filepath")
	// The line number in `code.filepath` best representing the operation. It SHOULD
	// point within the code unit named in `code.function`.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 42
	CodeLineNumberKey = attribute.Key("code.lineno")
	// The column number in `code.filepath` best representing the operation. It SHOULD
	// point within the code unit named in `code.function`.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 16
	CodeColumnKey = attribute.Key("code.column")
)

// This document defines semantic conventions for HTTP client and server Spans.
const (
	// HTTP request method.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'GET', 'POST', 'HEAD'
	HTTPMethodKey = attribute.Key("http.method")
	// [HTTP response status code](https://tools.ietf.org/html/rfc7231#section-6).
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If and only if one was received/sent.)
	// Stability: stable
	// Examples: 200
	HTTPStatusCodeKey = attribute.Key("http.status_code")
	// Kind of HTTP protocol used.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Note: If `net.transport` is not specified, it can be assumed to be `IP.TCP`
	// except if `http.flavor` is `QUIC`, in which case `IP.UDP` is assumed.
	HTTPFlavorKey = attribute.Key("http.flavor")
	// Value of the [HTTP User-Agent](https://www.rfc-
	// editor.org/rfc/rfc9110.html#field.user-agent) header sent by the client.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'CERN-LineMode/2.15 libwww/2.17b3'
	HTTPUserAgentKey = attribute.Key("http.user_agent")
	// The size of the request payload body in bytes. This is the number of bytes
	// transferred excluding headers and is often, but not always, present as the
	// [Content-Length](https://www.rfc-editor.org/rfc/rfc9110.html#field.content-
	// length) header. For requests using transport encoding, this should be the
	// compressed size.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 3495
	HTTPRequestContentLengthKey = attribute.Key("http.request_content_length")
	// The size of the response payload body in bytes. This is the number of bytes
	// transferred excluding headers and is often, but not always, present as the
	// [Content-Length](https://www.rfc-editor.org/rfc/rfc9110.html#field.content-
	// length) header. For requests using transport encoding, this should be the
	// compressed size.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 3495
	HTTPResponseContentLengthKey = attribute.Key("http.response_content_length")
)

var (
	// HTTP/1.0
	HTTPFlavorHTTP10 = HTTPFlavorKey.String("1.0")
	// HTTP/1.1
	HTTPFlavorHTTP11 = HTTPFlavorKey.String("1.1")
	// HTTP/2
	HTTPFlavorHTTP20 = HTTPFlavorKey.String("2.0")
	// HTTP/3
	HTTPFlavorHTTP30 = HTTPFlavorKey.String("3.0")
	// SPDY protocol
	HTTPFlavorSPDY = HTTPFlavorKey.String("SPDY")
	// QUIC protocol
	HTTPFlavorQUIC = HTTPFlavorKey.String("QUIC")
)

// Semantic Convention for HTTP Client
const (
	// Full HTTP request URL in the form `scheme://host[:port]/path?query[#fragment]`.
	// Usually the fragment is not transmitted over HTTP, but if it is known, it
	// should be included nevertheless.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'https://www.foo.bar/search?q=OpenTelemetry#SemConv'
	// Note: `http.url` MUST NOT contain credentials passed via URL in form of
	// `https://username:password@www.example.com/`. In such case the attribute's
	// value should be `https://www.example.com/`.
	HTTPURLKey = attribute.Key("http.url")
	// The ordinal number of request resending attempt (for any reason, including
	// redirects).
	//
	// Type: int
	// RequirementLevel: Recommended (if and only if request was retried.)
	// Stability: stable
	// Examples: 3
	// Note: The resend count SHOULD be updated each time an HTTP request gets resent
	// by the client, regardless of what was the cause of the resending (e.g.
	// redirection, authorization failure, 503 Server Unavailable, network issues, or
	// any other).
	HTTPResendCountKey = attribute.Key("http.resend_count")
)

// Semantic Convention for HTTP Server
const (
	// The URI scheme identifying the used protocol.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'http', 'https'
	HTTPSchemeKey = attribute.Key("http.scheme")
	// The full request target as passed in a HTTP request line or equivalent.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: '/path/12314/?q=ddds'
	HTTPTargetKey = attribute.Key("http.target")
	// The matched route (path template in the format used by the respective server
	// framework). See note below
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If and only if it's available)
	// Stability: stable
	// Examples: '/users/:userID?', '{controller}/{action}/{id?}'
	// Note: 'http.route' MUST NOT be populated when this is not supported by the HTTP
	// server framework as the route attribute should have low-cardinality and the URI
	// path can NOT substitute it.
	HTTPRouteKey = attribute.Key("http.route")
	// The IP address of the original client behind all proxies, if known (e.g. from
	// [X-Forwarded-For](https://developer.mozilla.org/en-
	// US/docs/Web/HTTP/Headers/X-Forwarded-For)).
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '83.164.160.102'
	// Note: This is not necessarily the same as `net.sock.peer.addr`, which would
	// identify the network-level peer, which may be a proxy.

	// This attribute should be set when a source of information different
	// from the one used for `net.sock.peer.addr`, is available even if that other
	// source just confirms the same value as `net.sock.peer.addr`.
	// Rationale: For `net.sock.peer.addr`, one typically does not know if it
	// comes from a proxy, reverse proxy, or the actual client. Setting
	// `http.client_ip` when it's the same as `net.sock.peer.addr` means that
	// one is at least somewhat confident that the address is not that of
	// the closest proxy.
	HTTPClientIPKey = attribute.Key("http.client_ip")
)

// Attributes that exist for multiple DynamoDB request types.
const (
	// The keys in the `RequestItems` object field.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Users', 'Cats'
	AWSDynamoDBTableNamesKey = attribute.Key("aws.dynamodb.table_names")
	// The JSON-serialized value of each item in the `ConsumedCapacity` response
	// field.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "CapacityUnits": number, "GlobalSecondaryIndexes": { "string" : {
	// "CapacityUnits": number, "ReadCapacityUnits": number, "WriteCapacityUnits":
	// number } }, "LocalSecondaryIndexes": { "string" : { "CapacityUnits": number,
	// "ReadCapacityUnits": number, "WriteCapacityUnits": number } },
	// "ReadCapacityUnits": number, "Table": { "CapacityUnits": number,
	// "ReadCapacityUnits": number, "WriteCapacityUnits": number }, "TableName":
	// "string", "WriteCapacityUnits": number }'
	AWSDynamoDBConsumedCapacityKey = attribute.Key("aws.dynamodb.consumed_capacity")
	// The JSON-serialized value of the `ItemCollectionMetrics` response field.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "string" : [ { "ItemCollectionKey": { "string" : { "B": blob,
	// "BOOL": boolean, "BS": [ blob ], "L": [ "AttributeValue" ], "M": { "string" :
	// "AttributeValue" }, "N": "string", "NS": [ "string" ], "NULL": boolean, "S":
	// "string", "SS": [ "string" ] } }, "SizeEstimateRangeGB": [ number ] } ] }'
	AWSDynamoDBItemCollectionMetricsKey = attribute.Key("aws.dynamodb.item_collection_metrics")
	// The value of the `ProvisionedThroughput.ReadCapacityUnits` request parameter.
	//
	// Type: double
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 1.0, 2.0
	AWSDynamoDBProvisionedReadCapacityKey = attribute.Key("aws.dynamodb.provisioned_read_capacity")
	// The value of the `ProvisionedThroughput.WriteCapacityUnits` request parameter.
	//
	// Type: double
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 1.0, 2.0
	AWSDynamoDBProvisionedWriteCapacityKey = attribute.Key("aws.dynamodb.provisioned_write_capacity")
	// The value of the `ConsistentRead` request parameter.
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	AWSDynamoDBConsistentReadKey = attribute.Key("aws.dynamodb.consistent_read")
	// The value of the `ProjectionExpression` request parameter.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Title', 'Title, Price, Color', 'Title, Description, RelatedItems,
	// ProductReviews'
	AWSDynamoDBProjectionKey = attribute.Key("aws.dynamodb.projection")
	// The value of the `Limit` request parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 10
	AWSDynamoDBLimitKey = attribute.Key("aws.dynamodb.limit")
	// The value of the `AttributesToGet` request parameter.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'lives', 'id'
	AWSDynamoDBAttributesToGetKey = attribute.Key("aws.dynamodb.attributes_to_get")
	// The value of the `IndexName` request parameter.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'name_to_group'
	AWSDynamoDBIndexNameKey = attribute.Key("aws.dynamodb.index_name")
	// The value of the `Select` request parameter.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'ALL_ATTRIBUTES', 'COUNT'
	AWSDynamoDBSelectKey = attribute.Key("aws.dynamodb.select")
)

// DynamoDB.CreateTable
const (
	// The JSON-serialized value of each item of the `GlobalSecondaryIndexes` request
	// field
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "IndexName": "string", "KeySchema": [ { "AttributeName": "string",
	// "KeyType": "string" } ], "Projection": { "NonKeyAttributes": [ "string" ],
	// "ProjectionType": "string" }, "ProvisionedThroughput": { "ReadCapacityUnits":
	// number, "WriteCapacityUnits": number } }'
	AWSDynamoDBGlobalSecondaryIndexesKey = attribute.Key("aws.dynamodb.global_secondary_indexes")
	// The JSON-serialized value of each item of the `LocalSecondaryIndexes` request
	// field.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "IndexARN": "string", "IndexName": "string", "IndexSizeBytes":
	// number, "ItemCount": number, "KeySchema": [ { "AttributeName": "string",
	// "KeyType": "string" } ], "Projection": { "NonKeyAttributes": [ "string" ],
	// "ProjectionType": "string" } }'
	AWSDynamoDBLocalSecondaryIndexesKey = attribute.Key("aws.dynamodb.local_secondary_indexes")
)

// DynamoDB.ListTables
const (
	// The value of the `ExclusiveStartTableName` request parameter.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Users', 'CatsTable'
	AWSDynamoDBExclusiveStartTableKey = attribute.Key("aws.dynamodb.exclusive_start_table")
	// The the number of items in the `TableNames` response parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 20
	AWSDynamoDBTableCountKey = attribute.Key("aws.dynamodb.table_count")
)

// DynamoDB.Query
const (
	// The value of the `ScanIndexForward` request parameter.
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	AWSDynamoDBScanForwardKey = attribute.Key("aws.dynamodb.scan_forward")
)

// DynamoDB.Scan
const (
	// The value of the `Segment` request parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 10
	AWSDynamoDBSegmentKey = attribute.Key("aws.dynamodb.segment")
	// The value of the `TotalSegments` request parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 100
	AWSDynamoDBTotalSegmentsKey = attribute.Key("aws.dynamodb.total_segments")
	// The value of the `Count` response parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 10
	AWSDynamoDBCountKey = attribute.Key("aws.dynamodb.count")
	// The value of the `ScannedCount` response parameter.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 50
	AWSDynamoDBScannedCountKey = attribute.Key("aws.dynamodb.scanned_count")
)

// DynamoDB.UpdateTable
const (
	// The JSON-serialized value of each item in the `AttributeDefinitions` request
	// field.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "AttributeName": "string", "AttributeType": "string" }'
	AWSDynamoDBAttributeDefinitionsKey = attribute.Key("aws.dynamodb.attribute_definitions")
	// The JSON-serialized value of each item in the the `GlobalSecondaryIndexUpdates`
	// request field.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '{ "Create": { "IndexName": "string", "KeySchema": [ {
	// "AttributeName": "string", "KeyType": "string" } ], "Projection": {
	// "NonKeyAttributes": [ "string" ], "ProjectionType": "string" },
	// "ProvisionedThroughput": { "ReadCapacityUnits": number, "WriteCapacityUnits":
	// number } }'
	AWSDynamoDBGlobalSecondaryIndexUpdatesKey = attribute.Key("aws.dynamodb.global_secondary_index_updates")
)

// This document defines semantic conventions to apply when instrumenting the GraphQL implementation. They map GraphQL operations to attributes on a Span.
const (
	// The name of the operation being executed.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'findBookByID'
	GraphqlOperationNameKey = attribute.Key("graphql.operation.name")
	// The type of the operation being executed.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'query', 'mutation', 'subscription'
	GraphqlOperationTypeKey = attribute.Key("graphql.operation.type")
	// The GraphQL document being executed.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'query findBookByID { bookByID(id: ?) { name } }'
	// Note: The value may be sanitized to exclude sensitive information.
	GraphqlDocumentKey = attribute.Key("graphql.document")
)

var (
	// GraphQL query
	GraphqlOperationTypeQuery = GraphqlOperationTypeKey.String("query")
	// GraphQL mutation
	GraphqlOperationTypeMutation = GraphqlOperationTypeKey.String("mutation")
	// GraphQL subscription
	GraphqlOperationTypeSubscription = GraphqlOperationTypeKey.String("subscription")
)

// Semantic convention describing per-message attributes populated on messaging spans or links.
const (
	// A value used by the messaging system as an identifier for the message,
	// represented as a string.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '452a7c7c7c7048c2f887f61572b18fc2'
	MessagingMessageIDKey = attribute.Key("messaging.message.id")
	// The [conversation ID](#conversations) identifying the conversation to which the
	// message belongs, represented as a string. Sometimes called "Correlation ID".
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'MyConversationID'
	MessagingMessageConversationIDKey = attribute.Key("messaging.message.conversation_id")
	// The (uncompressed) size of the message payload in bytes. Also use this
	// attribute if it is unknown whether the compressed or uncompressed payload size
	// is reported.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 2738
	MessagingMessagePayloadSizeBytesKey = attribute.Key("messaging.message.payload_size_bytes")
	// The compressed size of the message payload in bytes.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 2048
	MessagingMessagePayloadCompressedSizeBytesKey = attribute.Key("messaging.message.payload_compressed_size_bytes")
)

// Semantic convention for attributes that describe messaging destination on broker
const (
	// The message destination name
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'MyQueue', 'MyTopic'
	// Note: Destination name SHOULD uniquely identify a specific queue, topic or
	// other entity within the broker. If
	// the broker does not have such notion, the destination name SHOULD uniquely
	// identify the broker.
	MessagingDestinationNameKey = attribute.Key("messaging.destination.name")
	// The kind of message destination
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	MessagingDestinationKindKey = attribute.Key("messaging.destination.kind")
	// Low cardinality representation of the messaging destination name
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '/customers/{customerID}'
	// Note: Destination names could be constructed from templates. An example would
	// be a destination name involving a user name or product id. Although the
	// destination name in this case is of high cardinality, the underlying template
	// is of low cardinality and can be effectively used for grouping and aggregation.
	MessagingDestinationTemplateKey = attribute.Key("messaging.destination.template")
	// A boolean that is true if the message destination is temporary and might not
	// exist anymore after messages are processed.
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	MessagingDestinationTemporaryKey = attribute.Key("messaging.destination.temporary")
	// A boolean that is true if the message destination is anonymous (could be
	// unnamed or have auto-generated name).
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	MessagingDestinationAnonymousKey = attribute.Key("messaging.destination.anonymous")
)

var (
	// A message sent to a queue
	MessagingDestinationKindQueue = MessagingDestinationKindKey.String("queue")
	// A message sent to a topic
	MessagingDestinationKindTopic = MessagingDestinationKindKey.String("topic")
)

// Semantic convention for attributes that describe messaging source on broker
const (
	// The message source name
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'MyQueue', 'MyTopic'
	// Note: Source name SHOULD uniquely identify a specific queue, topic, or other
	// entity within the broker. If
	// the broker does not have such notion, the source name SHOULD uniquely identify
	// the broker.
	MessagingSourceNameKey = attribute.Key("messaging.source.name")
	// The kind of message source
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	MessagingSourceKindKey = attribute.Key("messaging.source.kind")
	// Low cardinality representation of the messaging source name
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '/customers/{customerID}'
	// Note: Source names could be constructed from templates. An example would be a
	// source name involving a user name or product id. Although the source name in
	// this case is of high cardinality, the underlying template is of low cardinality
	// and can be effectively used for grouping and aggregation.
	MessagingSourceTemplateKey = attribute.Key("messaging.source.template")
	// A boolean that is true if the message source is temporary and might not exist
	// anymore after messages are processed.
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	MessagingSourceTemporaryKey = attribute.Key("messaging.source.temporary")
	// A boolean that is true if the message source is anonymous (could be unnamed or
	// have auto-generated name).
	//
	// Type: boolean
	// RequirementLevel: Optional
	// Stability: stable
	MessagingSourceAnonymousKey = attribute.Key("messaging.source.anonymous")
)

var (
	// A message received from a queue
	MessagingSourceKindQueue = MessagingSourceKindKey.String("queue")
	// A message received from a topic
	MessagingSourceKindTopic = MessagingSourceKindKey.String("topic")
)

// This document defines general attributes used in messaging systems.
const (
	// A string identifying the messaging system.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'kafka', 'rabbitmq', 'rocketmq', 'activemq', 'AmazonSQS'
	MessagingSystemKey = attribute.Key("messaging.system")
	// A string identifying the kind of messaging operation as defined in the
	// [Operation names](#operation-names) section above.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	// Note: If a custom value is used, it MUST be of low cardinality.
	MessagingOperationKey = attribute.Key("messaging.operation")
	// The number of messages sent, received, or processed in the scope of the
	// batching operation.
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If the span describes an operation on
	// a batch of messages.)
	// Stability: stable
	// Examples: 0, 1, 2
	// Note: Instrumentations SHOULD NOT set `messaging.batch.message_count` on spans
	// that operate with a single message. When a messaging client library supports
	// both batch and single-message API for the same operation, instrumentations
	// SHOULD use `messaging.batch.message_count` for batching APIs and SHOULD NOT use
	// it for single-message APIs.
	MessagingBatchMessageCountKey = attribute.Key("messaging.batch.message_count")
)

var (
	// publish
	MessagingOperationPublish = MessagingOperationKey.String("publish")
	// receive
	MessagingOperationReceive = MessagingOperationKey.String("receive")
	// process
	MessagingOperationProcess = MessagingOperationKey.String("process")
)

// Semantic convention for a consumer of messages received from a messaging system
const (
	// The identifier for the consumer receiving a message. For Kafka, set it to
	// `{messaging.kafka.consumer.group} - {messaging.kafka.client_id}`, if both are
	// present, or only `messaging.kafka.consumer.group`. For brokers, such as
	// RabbitMQ and Artemis, set it to the `client_id` of the client consuming the
	// message.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'mygroup - client-6'
	MessagingConsumerIDKey = attribute.Key("messaging.consumer.id")
)

// Attributes for RabbitMQ
const (
	// RabbitMQ message routing key.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If not empty.)
	// Stability: stable
	// Examples: 'myKey'
	MessagingRabbitmqDestinationRoutingKeyKey = attribute.Key("messaging.rabbitmq.destination.routing_key")
)

// Attributes for Apache Kafka
const (
	// Message keys in Kafka are used for grouping alike messages to ensure they're
	// processed on the same partition. They differ from `messaging.message.id` in
	// that they're not unique. If the key is `null`, the attribute MUST NOT be set.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'myKey'
	// Note: If the key type is not string, it's string representation has to be
	// supplied for the attribute. If the key has no unambiguous, canonical string
	// form, don't include its value.
	MessagingKafkaMessageKeyKey = attribute.Key("messaging.kafka.message.key")
	// Name of the Kafka Consumer Group that is handling the message. Only applies to
	// consumers, not producers.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'my-group'
	MessagingKafkaConsumerGroupKey = attribute.Key("messaging.kafka.consumer.group")
	// Client ID for the Consumer or Producer that is handling the message.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'client-5'
	MessagingKafkaClientIDKey = attribute.Key("messaging.kafka.client_id")
	// Partition the message is sent to.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 2
	MessagingKafkaDestinationPartitionKey = attribute.Key("messaging.kafka.destination.partition")
	// Partition the message is received from.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 2
	MessagingKafkaSourcePartitionKey = attribute.Key("messaging.kafka.source.partition")
	// The offset of a record in the corresponding Kafka partition.
	//
	// Type: int
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 42
	MessagingKafkaMessageOffsetKey = attribute.Key("messaging.kafka.message.offset")
	// A boolean that is true if the message is a tombstone.
	//
	// Type: boolean
	// RequirementLevel: ConditionallyRequired (If value is `true`. When missing, the
	// value is assumed to be `false`.)
	// Stability: stable
	MessagingKafkaMessageTombstoneKey = attribute.Key("messaging.kafka.message.tombstone")
)

// Attributes for Apache RocketMQ
const (
	// Namespace of RocketMQ resources, resources in different namespaces are
	// individual.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'myNamespace'
	MessagingRocketmqNamespaceKey = attribute.Key("messaging.rocketmq.namespace")
	// Name of the RocketMQ producer/consumer group that is handling the message. The
	// client type is identified by the SpanKind.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'myConsumerGroup'
	MessagingRocketmqClientGroupKey = attribute.Key("messaging.rocketmq.client_group")
	// The unique identifier for each client.
	//
	// Type: string
	// RequirementLevel: Required
	// Stability: stable
	// Examples: 'myhost@8742@s8083jm'
	MessagingRocketmqClientIDKey = attribute.Key("messaging.rocketmq.client_id")
	// The timestamp in milliseconds that the delay message is expected to be
	// delivered to consumer.
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If the message type is delay and delay
	// time level is not specified.)
	// Stability: stable
	// Examples: 1665987217045
	MessagingRocketmqMessageDeliveryTimestampKey = attribute.Key("messaging.rocketmq.message.delivery_timestamp")
	// The delay time level for delay message, which determines the message delay
	// time.
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If the message type is delay and
	// delivery timestamp is not specified.)
	// Stability: stable
	// Examples: 3
	MessagingRocketmqMessageDelayTimeLevelKey = attribute.Key("messaging.rocketmq.message.delay_time_level")
	// It is essential for FIFO message. Messages that belong to the same message
	// group are always processed one by one within the same consumer group.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If the message type is FIFO.)
	// Stability: stable
	// Examples: 'myMessageGroup'
	MessagingRocketmqMessageGroupKey = attribute.Key("messaging.rocketmq.message.group")
	// Type of message.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	MessagingRocketmqMessageTypeKey = attribute.Key("messaging.rocketmq.message.type")
	// The secondary classifier of message besides topic.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'tagA'
	MessagingRocketmqMessageTagKey = attribute.Key("messaging.rocketmq.message.tag")
	// Key(s) of message, another way to mark message besides message id.
	//
	// Type: string[]
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'keyA', 'keyB'
	MessagingRocketmqMessageKeysKey = attribute.Key("messaging.rocketmq.message.keys")
	// Model of message consumption. This only applies to consumer spans.
	//
	// Type: Enum
	// RequirementLevel: Optional
	// Stability: stable
	MessagingRocketmqConsumptionModelKey = attribute.Key("messaging.rocketmq.consumption_model")
)

var (
	// Normal message
	MessagingRocketmqMessageTypeNormal = MessagingRocketmqMessageTypeKey.String("normal")
	// FIFO message
	MessagingRocketmqMessageTypeFifo = MessagingRocketmqMessageTypeKey.String("fifo")
	// Delay message
	MessagingRocketmqMessageTypeDelay = MessagingRocketmqMessageTypeKey.String("delay")
	// Transaction message
	MessagingRocketmqMessageTypeTransaction = MessagingRocketmqMessageTypeKey.String("transaction")
)

var (
	// Clustering consumption model
	MessagingRocketmqConsumptionModelClustering = MessagingRocketmqConsumptionModelKey.String("clustering")
	// Broadcasting consumption model
	MessagingRocketmqConsumptionModelBroadcasting = MessagingRocketmqConsumptionModelKey.String("broadcasting")
)

// This document defines semantic conventions for remote procedure calls.
const (
	// A string identifying the remoting system. See below for a list of well-known
	// identifiers.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	RPCSystemKey = attribute.Key("rpc.system")
	// The full (logical) name of the service being called, including its package
	// name, if applicable.
	//
	// Type: string
	// RequirementLevel: Recommended
	// Stability: stable
	// Examples: 'myservice.EchoService'
	// Note: This is the logical name of the service from the RPC interface
	// perspective, which can be different from the name of any implementing class.
	// The `code.namespace` attribute may be used to store the latter (despite the
	// attribute name, it may include a class name; e.g., class with method actually
	// executing the call on the server side, RPC client stub class on the client
	// side).
	RPCServiceKey = attribute.Key("rpc.service")
	// The name of the (logical) method being called, must be equal to the $method
	// part in the span name.
	//
	// Type: string
	// RequirementLevel: Recommended
	// Stability: stable
	// Examples: 'exampleMethod'
	// Note: This is the logical name of the method from the RPC interface
	// perspective, which can be different from the name of any implementing
	// method/function. The `code.function` attribute may be used to store the latter
	// (e.g., method actually executing the call on the server side, RPC client stub
	// method on the client side).
	RPCMethodKey = attribute.Key("rpc.method")
)

var (
	// gRPC
	RPCSystemGRPC = RPCSystemKey.String("grpc")
	// Java RMI
	RPCSystemJavaRmi = RPCSystemKey.String("java_rmi")
	// .NET WCF
	RPCSystemDotnetWcf = RPCSystemKey.String("dotnet_wcf")
	// Apache Dubbo
	RPCSystemApacheDubbo = RPCSystemKey.String("apache_dubbo")
)

// Tech-specific attributes for gRPC.
const (
	// The [numeric status
	// code](https://github.com/grpc/grpc/blob/v1.33.2/doc/statuscodes.md) of the gRPC
	// request.
	//
	// Type: Enum
	// RequirementLevel: Required
	// Stability: stable
	RPCGRPCStatusCodeKey = attribute.Key("rpc.grpc.status_code")
)

var (
	// OK
	RPCGRPCStatusCodeOk = RPCGRPCStatusCodeKey.Int(0)
	// CANCELLED
	RPCGRPCStatusCodeCancelled = RPCGRPCStatusCodeKey.Int(1)
	// UNKNOWN
	RPCGRPCStatusCodeUnknown = RPCGRPCStatusCodeKey.Int(2)
	// INVALID_ARGUMENT
	RPCGRPCStatusCodeInvalidArgument = RPCGRPCStatusCodeKey.Int(3)
	// DEADLINE_EXCEEDED
	RPCGRPCStatusCodeDeadlineExceeded = RPCGRPCStatusCodeKey.Int(4)
	// NOT_FOUND
	RPCGRPCStatusCodeNotFound = RPCGRPCStatusCodeKey.Int(5)
	// ALREADY_EXISTS
	RPCGRPCStatusCodeAlreadyExists = RPCGRPCStatusCodeKey.Int(6)
	// PERMISSION_DENIED
	RPCGRPCStatusCodePermissionDenied = RPCGRPCStatusCodeKey.Int(7)
	// RESOURCE_EXHAUSTED
	RPCGRPCStatusCodeResourceExhausted = RPCGRPCStatusCodeKey.Int(8)
	// FAILED_PRECONDITION
	RPCGRPCStatusCodeFailedPrecondition = RPCGRPCStatusCodeKey.Int(9)
	// ABORTED
	RPCGRPCStatusCodeAborted = RPCGRPCStatusCodeKey.Int(10)
	// OUT_OF_RANGE
	RPCGRPCStatusCodeOutOfRange = RPCGRPCStatusCodeKey.Int(11)
	// UNIMPLEMENTED
	RPCGRPCStatusCodeUnimplemented = RPCGRPCStatusCodeKey.Int(12)
	// INTERNAL
	RPCGRPCStatusCodeInternal = RPCGRPCStatusCodeKey.Int(13)
	// UNAVAILABLE
	RPCGRPCStatusCodeUnavailable = RPCGRPCStatusCodeKey.Int(14)
	// DATA_LOSS
	RPCGRPCStatusCodeDataLoss = RPCGRPCStatusCodeKey.Int(15)
	// UNAUTHENTICATED
	RPCGRPCStatusCodeUnauthenticated = RPCGRPCStatusCodeKey.Int(16)
)

// Tech-specific attributes for [JSON RPC](https://www.jsonrpc.org/).
const (
	// Protocol version as in `jsonrpc` property of request/response. Since JSON-RPC
	// 1.0 does not specify this, the value can be omitted.
	//
	// Type: string
	// RequirementLevel: ConditionallyRequired (If other than the default version
	// (`1.0`))
	// Stability: stable
	// Examples: '2.0', '1.0'
	RPCJsonrpcVersionKey = attribute.Key("rpc.jsonrpc.version")
	// `id` property of request or response. Since protocol allows id to be int,
	// string, `null` or missing (for notifications), value is expected to be cast to
	// string for simplicity. Use empty string in case of `null` value. Omit entirely
	// if this is a notification.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: '10', 'request-7', ''
	RPCJsonrpcRequestIDKey = attribute.Key("rpc.jsonrpc.request_id")
	// `error.code` property of response if it is an error response.
	//
	// Type: int
	// RequirementLevel: ConditionallyRequired (If response is not successful.)
	// Stability: stable
	// Examples: -32700, 100
	RPCJsonrpcErrorCodeKey = attribute.Key("rpc.jsonrpc.error_code")
	// `error.message` property of response if it is an error response.
	//
	// Type: string
	// RequirementLevel: Optional
	// Stability: stable
	// Examples: 'Parse error', 'User already exists'
	RPCJsonrpcErrorMessageKey = attribute.Key("rpc.jsonrpc.error_message")
)
