
```shell
$ k --context kind-kubeflex -n kubeflow exec -it mysql-7d8b8ff4f4-mpglt -- sh

mysql> use mlpipeline;
mysql> SHOW columns FROM run_details;
+-------------------------+--------------+------+-----+---------+-------+
| Field                   | Type         | Null | Key | Default | Extra |
+-------------------------+--------------+------+-----+---------+-------+
| UUID                    | varchar(255) | NO   | PRI | NULL    |       |
| DisplayName             | varchar(255) | NO   |     | NULL    |       |
| Name                    | varchar(255) | NO   |     | NULL    |       |
| Description             | varchar(255) | NO   |     | NULL    |       |
| Namespace               | varchar(255) | NO   | MUL | NULL    |       |
| ExperimentUUID          | varchar(255) | NO   | MUL | NULL    |       |
| JobUUID                 | varchar(255) | YES  |     | NULL    |       |
| StorageState            | varchar(255) | NO   |     | NULL    |       |
| ServiceAccount          | varchar(255) | NO   |     | NULL    |       |
| PipelineId              | varchar(255) | NO   |     | NULL    |       |
| PipelineVersionId       | varchar(255) | YES  |     | NULL    |       |
| PipelineName            | varchar(255) | NO   |     | NULL    |       |
| PipelineSpecManifest    | longtext     | YES  |     | NULL    |       |
| WorkflowSpecManifest    | longtext     | NO   |     | NULL    |       |
| Parameters              | longtext     | YES  |     | NULL    |       |
| RuntimeParameters       | longtext     | YES  |     | NULL    |       |
| PipelineRoot            | longtext     | YES  |     | NULL    |       |
| CreatedAtInSec          | bigint       | NO   |     | NULL    |       |
| ScheduledAtInSec        | bigint       | YES  |     | 0       |       |
| FinishedAtInSec         | bigint       | YES  |     | 0       |       |
| Conditions              | varchar(255) | NO   |     | NULL    |       |
| State                   | varchar(255) | YES  |     | NULL    |       |
| StateHistory            | longtext     | YES  |     | NULL    |       |
| PipelineRuntimeManifest | longtext     | NO   |     | NULL    |       |
| WorkflowRuntimeManifest | longtext     | NO   |     | NULL    |       |
| PipelineContextId       | bigint       | YES  |     | 0       |       |
| PipelineRunContextId    | bigint       | YES  |     | 0       |       |
+-------------------------+--------------+------+-----+---------+-------+
27 rows in set (0.01 sec)

mysql> SELECT UUID FROM run_details LIMIT 5;
+--------------------------------------+
| UUID                                 |
+--------------------------------------+
| 82fa5a77-f6a3-4479-babb-44943def31ce |
| 51f511cc-8b80-4a41-b18e-4b0ffea0b008 |
| dd919151-2818-4280-a89a-2771867a022f |
| 3f5c6aea-ba84-4fa4-8f04-f70db95d7bee |
| 6459d29a-5b31-4b35-934b-6d56f348f7fd |
+--------------------------------------+
5 rows in set (0.00 sec)

mysql> SELECT UUID FROM run_details;
+--------------------------------------+
| UUID                                 |
+--------------------------------------+
| 82fa5a77-f6a3-4479-babb-44943def31ce |
| 51f511cc-8b80-4a41-b18e-4b0ffea0b008 |
| dd919151-2818-4280-a89a-2771867a022f |
| 3f5c6aea-ba84-4fa4-8f04-f70db95d7bee |
| 6459d29a-5b31-4b35-934b-6d56f348f7fd |
| 75dca3e7-b289-480a-a677-d3d113272c08 |
+--------------------------------------+
6 rows in set (0.01 sec)

mysql> SELECT UUID,NAME FROM run_details;
+--------------------------------------+----------------------+
| UUID                                 | NAME                 |
+--------------------------------------+----------------------+
| 3f5c6aea-ba84-4fa4-8f04-f70db95d7bee | my-pipeline-llrcn    |
| 51f511cc-8b80-4a41-b18e-4b0ffea0b008 | my-pipeline-mjxtr    |
| 6459d29a-5b31-4b35-934b-6d56f348f7fd | my-pipeline-7kvfh    |
| 75dca3e7-b289-480a-a677-d3d113272c08 | my-pipeline-8q9sv    |
| 82fa5a77-f6a3-4479-babb-44943def31ce | hello-pipeline-q7g9z |
| dd919151-2818-4280-a89a-2771867a022f | my-pipeline-566wn    |
+--------------------------------------+----------------------+
6 rows in set (0.00 sec)

mysql> SELECT UUID,DISPLAYNAME,NAME FROM run_details;
+--------------------------------------+-----------------------------------+----------------------+
| UUID                                 | DISPLAYNAME                       | NAME                 |
+--------------------------------------+-----------------------------------+----------------------+
| 3f5c6aea-ba84-4fa4-8f04-f70db95d7bee | pipeline.yaml 2024-03-21 11-05-58 | my-pipeline-llrcn    |
| 51f511cc-8b80-4a41-b18e-4b0ffea0b008 | pipeline.yaml 2024-03-20 23-08-08 | my-pipeline-mjxtr    |
| 6459d29a-5b31-4b35-934b-6d56f348f7fd | pipeline.yaml 2024-03-21 11-38-20 | my-pipeline-7kvfh    |
| 75dca3e7-b289-480a-a677-d3d113272c08 | pipeline.yaml 2024-03-21 15-41-10 | my-pipeline-8q9sv    |
| 82fa5a77-f6a3-4479-babb-44943def31ce | pipeline.yaml 2024-03-20 22-48-13 | hello-pipeline-q7g9z |
| dd919151-2818-4280-a89a-2771867a022f | pipeline.yaml 2024-03-20 23-20-13 | my-pipeline-566wn    |
+--------------------------------------+-----------------------------------+----------------------+
6 rows in set (0.00 sec)

mysql> SELECT UUID,DISPLAYNAME,NAME,CreatedAtInSec FROM run_details;
+--------------------------------------+-----------------------------------+----------------------+----------------+
| UUID                                 | DISPLAYNAME                       | NAME                 | CreatedAtInSec |
+--------------------------------------+-----------------------------------+----------------------+----------------+
| 3f5c6aea-ba84-4fa4-8f04-f70db95d7bee | pipeline.yaml 2024-03-21 11-05-58 | my-pipeline-llrcn    |     1711033558 |
| 51f511cc-8b80-4a41-b18e-4b0ffea0b008 | pipeline.yaml 2024-03-20 23-08-08 | my-pipeline-mjxtr    |     1710990487 |
| 6459d29a-5b31-4b35-934b-6d56f348f7fd | pipeline.yaml 2024-03-21 11-38-20 | my-pipeline-7kvfh    |     1711035500 |
| 75dca3e7-b289-480a-a677-d3d113272c08 | pipeline.yaml 2024-03-21 15-41-10 | my-pipeline-8q9sv    |     1711050069 |
| 82fa5a77-f6a3-4479-babb-44943def31ce | pipeline.yaml 2024-03-20 22-48-13 | hello-pipeline-q7g9z |     1710989292 |
| dd919151-2818-4280-a89a-2771867a022f | pipeline.yaml 2024-03-20 23-20-13 | my-pipeline-566wn    |     1710991212 |
+--------------------------------------+-----------------------------------+----------------------+----------------+
6 rows in set (0.01 sec)


k --context kind-kubeflex -n kubeflow exec -it mysql-7d8b8ff4f4-mpglt -- sh
$ mysql
mysql> show databases;
+--------------------+
| Database           |
+--------------------+
| cachedb            |
| information_schema |
| metadb             |
| mlpipeline         |
| mysql              |
| performance_schema |
| sys                |
+--------------------+

mysql> use metadb;

mysql> show tables;
+-------------------+
| Tables_in_metadb  |
+-------------------+
| Artifact          |
| ArtifactProperty  |
| Association       |
| Attribution       |
| Context           |
| ContextProperty   |
| Event             |
| EventPath         |
| Execution         |
| ExecutionProperty |
| MLMDEnv           |
| ParentContext     |
| ParentType        |
| Type              |
| TypeProperty      |
+-------------------+
15 rows in set (0.01 sec)

mysql> show columns from Artifact;
+------------------------------+--------------+------+-----+---------+----------------+
| Field                        | Type         | Null | Key | Default | Extra          |
+------------------------------+--------------+------+-----+---------+----------------+
| id                           | int          | NO   | PRI | NULL    | auto_increment |
| type_id                      | int          | NO   | MUL | NULL    |                |
| uri                          | text         | YES  | MUL | NULL    |                |
| state                        | int          | YES  |     | NULL    |                |
| name                         | varchar(255) | YES  |     | NULL    |                |
| external_id                  | varchar(255) | YES  | UNI | NULL    |                |
| create_time_since_epoch      | bigint       | NO   | MUL | 0       |                |
| last_update_time_since_epoch | bigint       | NO   | MUL | 0       |                |
+------------------------------+--------------+------+-----+---------+----------------+

mysql> show columns from Execution;
+------------------------------+--------------+------+-----+---------+----------------+
| Field                        | Type         | Null | Key | Default | Extra          |
+------------------------------+--------------+------+-----+---------+----------------+
| id                           | int          | NO   | PRI | NULL    | auto_increment |
| type_id                      | int          | NO   | MUL | NULL    |                |
| last_known_state             | int          | YES  |     | NULL    |                |
| name                         | varchar(255) | YES  |     | NULL    |                |
| external_id                  | varchar(255) | YES  | UNI | NULL    |                |
| create_time_since_epoch      | bigint       | NO   | MUL | 0       |                |
| last_update_time_since_epoch | bigint       | NO   | MUL | 0       |                |
+------------------------------+--------------+------+-----+---------+----------------+

mysql> select id,name from Execution;
+----+------------------------------------------+
| id | name                                     |
+----+------------------------------------------+
|  3 | run/51f511cc-8b80-4a41-b18e-4b0ffea0b008 |
|  1 | run/82fa5a77-f6a3-4479-babb-44943def31ce |
|  2 | NULL                                     |
|  4 | NULL                                     |
+----+------------------------------------------+
4 rows in set (0.00 sec)
```