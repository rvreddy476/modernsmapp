class GroupRule {
  final String id;
  final String groupId;
  final int ruleOrder;
  final String title;
  final String description;
  final DateTime createdAt;

  const GroupRule({
    required this.id,
    required this.groupId,
    this.ruleOrder = 0,
    required this.title,
    this.description = '',
    required this.createdAt,
  });

  factory GroupRule.fromJson(Map<String, dynamic> json) {
    return GroupRule(
      id: json['id'] as String? ?? '',
      groupId: json['group_id'] as String? ?? '',
      ruleOrder: json['rule_order'] as int? ?? 0,
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
