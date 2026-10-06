name: 功能建议
description: 提出新能力或改进（请先读 docs/roadmap.md 的「不做清单」）
body:
  - type: textarea
    id: problem
    attributes:
      label: 要解决什么问题
      description: 场景优先：描述你的运维痛点，而不是解决方案
    validations:
      required: true
  - type: textarea
    id: proposal
    attributes:
      label: 期望的形态
    validations:
      required: true
  - type: checkboxes
    id: constraints
    attributes:
      label: 约束确认
      options:
        - label: 我已读过 roadmap 的「不做清单」，此项不在显式排除项内
          required: true
