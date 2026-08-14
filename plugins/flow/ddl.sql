-- bpm_flow_repository: one row per flow instance (state snapshot).
CREATE TABLE `bpm_flow_repository` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT 'ID 编号',
  `instant_id` varchar(64) NOT NULL COMMENT '流程实例id',
  `graph` json DEFAULT NULL COMMENT '实例图',
  `states` json DEFAULT NULL COMMENT '运行状态',
  `vars` json DEFAULT NULL COMMENT '运行变量',
  `creator` varchar(64) DEFAULT NULL COMMENT '创建人',
  `create_time` datetime DEFAULT NULL COMMENT '创建时间',
  `updater` varchar(64) DEFAULT NULL COMMENT '修改人',
  `update_time` datetime DEFAULT NULL COMMENT '修改时间',
  PRIMARY KEY (`id`) USING BTREE,
  UNIQUE KEY `uniq_instantId` (`instant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='工作流存储';

-- bpm_flow_task: task transition history (append-only audit).
CREATE TABLE `bpm_flow_task` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT 'ID 编号',
  `flow_ins_id` varchar(64) NOT NULL COMMENT '流程运行id',
  `proc_def_id` varchar(64) NOT NULL COMMENT '流程定义id',
  `task_id` varchar(64) NOT NULL COMMENT '任务id',
  `source_node_code` varchar(64) NOT NULL COMMENT '节点编码',
  `source_node_name` varchar(64) DEFAULT NULL COMMENT '任务名称',
  `source_node_type` varchar(64) NOT NULL COMMENT '节点类型',
  `target_node_code` varchar(64) NOT NULL COMMENT '目标节点编码',
  `target_node_name` varchar(64) NOT NULL COMMENT '目标节点名称',
  `target_node_type` varchar(64) NOT NULL COMMENT '目标节点类型',
  `assignee` varchar(64) NOT NULL COMMENT '办理人',
  `status` int DEFAULT NULL COMMENT '状态',
  `form_id` bigint DEFAULT NULL COMMENT '表单id',
  `variables` json DEFAULT NULL COMMENT '变量',
  `message` text COMMENT '处理消息',
  `creator` varchar(64) DEFAULT NULL COMMENT '创建人',
  `create_time` datetime DEFAULT NULL COMMENT '创建时间',
  `updater` varchar(64) DEFAULT NULL COMMENT '修改人',
  `update_time` datetime DEFAULT NULL COMMENT '修改时间',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='工作流任务历史';
