name: Bug 报告
description: 报告平台缺陷
body:
  - type: textarea
    id: what-happened
    attributes:
      label: 现象描述
      description: 发生了什么、期望是什么
    validations:
      required: true
  - type: input
    id: version
    attributes:
      label: 平台版本
      placeholder: 如 v0.12.0（管理后台页脚可见）
    validations:
      required: true
  - type: textarea
    id: repro
    attributes:
      label: 复现步骤
      placeholder: |
        1. 进入 ……
        2. 点击 ……
    validations:
      required: true
  - type: textarea
    id: logs
    attributes:
      label: 相关日志
      description: 后端日志 / 浏览器控制台（请先删除凭据等敏感信息）
  - type: input
    id: deploy
    attributes:
      label: 部署形态
      placeholder: 如 docker-compose 单机 / k3s 双轨
