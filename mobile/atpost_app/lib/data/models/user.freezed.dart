// coverage:ignore-file
// GENERATED CODE - DO NOT MODIFY BY HAND
// ignore_for_file: type=lint
// ignore_for_file: unused_element, deprecated_member_use, deprecated_member_use_from_same_package, use_function_type_syntax_for_parameters, unnecessary_const, avoid_init_to_null, invalid_override_different_default_values_named, prefer_expression_function_bodies, annotate_overrides, invalid_annotation_target, unnecessary_question_mark

part of 'user.dart';

// **************************************************************************
// FreezedGenerator
// **************************************************************************

T _$identity<T>(T value) => value;

final _privateConstructorUsedError = UnsupportedError(
  'It seems like you constructed your class using `MyClass._()`. This constructor is only meant to be used by freezed and you are not supposed to need it nor use it.\nPlease check the documentation here for more information: https://github.com/rrousselGit/freezed#adding-getters-and-methods-to-our-models',
);

/// @nodoc
mixin _$User {
  String get id => throw _privateConstructorUsedError;
  String get username => throw _privateConstructorUsedError;
  String get displayName => throw _privateConstructorUsedError;
  String get firstName => throw _privateConstructorUsedError;
  String get lastName => throw _privateConstructorUsedError;
  String? get bio => throw _privateConstructorUsedError;
  String? get pronouns => throw _privateConstructorUsedError;
  String? get avatarMediaId => throw _privateConstructorUsedError;
  String? get coverMediaId => throw _privateConstructorUsedError;
  String? get location => throw _privateConstructorUsedError;
  String? get profession => throw _privateConstructorUsedError;
  String? get website => throw _privateConstructorUsedError;
  bool get isVerified => throw _privateConstructorUsedError;
  int get followerCount => throw _privateConstructorUsedError;
  int get followingCount => throw _privateConstructorUsedError;
  int get friendCount => throw _privateConstructorUsedError;
  int get postCount => throw _privateConstructorUsedError;

  /// Create a copy of User
  /// with the given fields replaced by the non-null parameter values.
  @JsonKey(includeFromJson: false, includeToJson: false)
  $UserCopyWith<User> get copyWith => throw _privateConstructorUsedError;
}

/// @nodoc
abstract class $UserCopyWith<$Res> {
  factory $UserCopyWith(User value, $Res Function(User) then) =
      _$UserCopyWithImpl<$Res, User>;
  @useResult
  $Res call({
    String id,
    String username,
    String displayName,
    String firstName,
    String lastName,
    String? bio,
    String? pronouns,
    String? avatarMediaId,
    String? coverMediaId,
    String? location,
    String? profession,
    String? website,
    bool isVerified,
    int followerCount,
    int followingCount,
    int friendCount,
    int postCount,
  });
}

/// @nodoc
class _$UserCopyWithImpl<$Res, $Val extends User>
    implements $UserCopyWith<$Res> {
  _$UserCopyWithImpl(this._value, this._then);

  // ignore: unused_field
  final $Val _value;
  // ignore: unused_field
  final $Res Function($Val) _then;

  /// Create a copy of User
  /// with the given fields replaced by the non-null parameter values.
  @pragma('vm:prefer-inline')
  @override
  $Res call({
    Object? id = null,
    Object? username = null,
    Object? displayName = null,
    Object? firstName = null,
    Object? lastName = null,
    Object? bio = freezed,
    Object? pronouns = freezed,
    Object? avatarMediaId = freezed,
    Object? coverMediaId = freezed,
    Object? location = freezed,
    Object? profession = freezed,
    Object? website = freezed,
    Object? isVerified = null,
    Object? followerCount = null,
    Object? followingCount = null,
    Object? friendCount = null,
    Object? postCount = null,
  }) {
    return _then(
      _value.copyWith(
            id: null == id
                ? _value.id
                : id // ignore: cast_nullable_to_non_nullable
                      as String,
            username: null == username
                ? _value.username
                : username // ignore: cast_nullable_to_non_nullable
                      as String,
            displayName: null == displayName
                ? _value.displayName
                : displayName // ignore: cast_nullable_to_non_nullable
                      as String,
            firstName: null == firstName
                ? _value.firstName
                : firstName // ignore: cast_nullable_to_non_nullable
                      as String,
            lastName: null == lastName
                ? _value.lastName
                : lastName // ignore: cast_nullable_to_non_nullable
                      as String,
            bio: freezed == bio
                ? _value.bio
                : bio // ignore: cast_nullable_to_non_nullable
                      as String?,
            pronouns: freezed == pronouns
                ? _value.pronouns
                : pronouns // ignore: cast_nullable_to_non_nullable
                      as String?,
            avatarMediaId: freezed == avatarMediaId
                ? _value.avatarMediaId
                : avatarMediaId // ignore: cast_nullable_to_non_nullable
                      as String?,
            coverMediaId: freezed == coverMediaId
                ? _value.coverMediaId
                : coverMediaId // ignore: cast_nullable_to_non_nullable
                      as String?,
            location: freezed == location
                ? _value.location
                : location // ignore: cast_nullable_to_non_nullable
                      as String?,
            profession: freezed == profession
                ? _value.profession
                : profession // ignore: cast_nullable_to_non_nullable
                      as String?,
            website: freezed == website
                ? _value.website
                : website // ignore: cast_nullable_to_non_nullable
                      as String?,
            isVerified: null == isVerified
                ? _value.isVerified
                : isVerified // ignore: cast_nullable_to_non_nullable
                      as bool,
            followerCount: null == followerCount
                ? _value.followerCount
                : followerCount // ignore: cast_nullable_to_non_nullable
                      as int,
            followingCount: null == followingCount
                ? _value.followingCount
                : followingCount // ignore: cast_nullable_to_non_nullable
                      as int,
            friendCount: null == friendCount
                ? _value.friendCount
                : friendCount // ignore: cast_nullable_to_non_nullable
                      as int,
            postCount: null == postCount
                ? _value.postCount
                : postCount // ignore: cast_nullable_to_non_nullable
                      as int,
          )
          as $Val,
    );
  }
}

/// @nodoc
abstract class _$$UserImplCopyWith<$Res> implements $UserCopyWith<$Res> {
  factory _$$UserImplCopyWith(
    _$UserImpl value,
    $Res Function(_$UserImpl) then,
  ) = __$$UserImplCopyWithImpl<$Res>;
  @override
  @useResult
  $Res call({
    String id,
    String username,
    String displayName,
    String firstName,
    String lastName,
    String? bio,
    String? pronouns,
    String? avatarMediaId,
    String? coverMediaId,
    String? location,
    String? profession,
    String? website,
    bool isVerified,
    int followerCount,
    int followingCount,
    int friendCount,
    int postCount,
  });
}

/// @nodoc
class __$$UserImplCopyWithImpl<$Res>
    extends _$UserCopyWithImpl<$Res, _$UserImpl>
    implements _$$UserImplCopyWith<$Res> {
  __$$UserImplCopyWithImpl(_$UserImpl _value, $Res Function(_$UserImpl) _then)
    : super(_value, _then);

  /// Create a copy of User
  /// with the given fields replaced by the non-null parameter values.
  @pragma('vm:prefer-inline')
  @override
  $Res call({
    Object? id = null,
    Object? username = null,
    Object? displayName = null,
    Object? firstName = null,
    Object? lastName = null,
    Object? bio = freezed,
    Object? pronouns = freezed,
    Object? avatarMediaId = freezed,
    Object? coverMediaId = freezed,
    Object? location = freezed,
    Object? profession = freezed,
    Object? website = freezed,
    Object? isVerified = null,
    Object? followerCount = null,
    Object? followingCount = null,
    Object? friendCount = null,
    Object? postCount = null,
  }) {
    return _then(
      _$UserImpl(
        id: null == id
            ? _value.id
            : id // ignore: cast_nullable_to_non_nullable
                  as String,
        username: null == username
            ? _value.username
            : username // ignore: cast_nullable_to_non_nullable
                  as String,
        displayName: null == displayName
            ? _value.displayName
            : displayName // ignore: cast_nullable_to_non_nullable
                  as String,
        firstName: null == firstName
            ? _value.firstName
            : firstName // ignore: cast_nullable_to_non_nullable
                  as String,
        lastName: null == lastName
            ? _value.lastName
            : lastName // ignore: cast_nullable_to_non_nullable
                  as String,
        bio: freezed == bio
            ? _value.bio
            : bio // ignore: cast_nullable_to_non_nullable
                  as String?,
        pronouns: freezed == pronouns
            ? _value.pronouns
            : pronouns // ignore: cast_nullable_to_non_nullable
                  as String?,
        avatarMediaId: freezed == avatarMediaId
            ? _value.avatarMediaId
            : avatarMediaId // ignore: cast_nullable_to_non_nullable
                  as String?,
        coverMediaId: freezed == coverMediaId
            ? _value.coverMediaId
            : coverMediaId // ignore: cast_nullable_to_non_nullable
                  as String?,
        location: freezed == location
            ? _value.location
            : location // ignore: cast_nullable_to_non_nullable
                  as String?,
        profession: freezed == profession
            ? _value.profession
            : profession // ignore: cast_nullable_to_non_nullable
                  as String?,
        website: freezed == website
            ? _value.website
            : website // ignore: cast_nullable_to_non_nullable
                  as String?,
        isVerified: null == isVerified
            ? _value.isVerified
            : isVerified // ignore: cast_nullable_to_non_nullable
                  as bool,
        followerCount: null == followerCount
            ? _value.followerCount
            : followerCount // ignore: cast_nullable_to_non_nullable
                  as int,
        followingCount: null == followingCount
            ? _value.followingCount
            : followingCount // ignore: cast_nullable_to_non_nullable
                  as int,
        friendCount: null == friendCount
            ? _value.friendCount
            : friendCount // ignore: cast_nullable_to_non_nullable
                  as int,
        postCount: null == postCount
            ? _value.postCount
            : postCount // ignore: cast_nullable_to_non_nullable
                  as int,
      ),
    );
  }
}

/// @nodoc

class _$UserImpl extends _User {
  const _$UserImpl({
    this.id = '',
    this.username = 'user',
    this.displayName = 'VChat User',
    this.firstName = '',
    this.lastName = '',
    this.bio,
    this.pronouns,
    this.avatarMediaId,
    this.coverMediaId,
    this.location,
    this.profession,
    this.website,
    this.isVerified = false,
    this.followerCount = 0,
    this.followingCount = 0,
    this.friendCount = 0,
    this.postCount = 0,
  }) : super._();

  @override
  @JsonKey()
  final String id;
  @override
  @JsonKey()
  final String username;
  @override
  @JsonKey()
  final String displayName;
  @override
  @JsonKey()
  final String firstName;
  @override
  @JsonKey()
  final String lastName;
  @override
  final String? bio;
  @override
  final String? pronouns;
  @override
  final String? avatarMediaId;
  @override
  final String? coverMediaId;
  @override
  final String? location;
  @override
  final String? profession;
  @override
  final String? website;
  @override
  @JsonKey()
  final bool isVerified;
  @override
  @JsonKey()
  final int followerCount;
  @override
  @JsonKey()
  final int followingCount;
  @override
  @JsonKey()
  final int friendCount;
  @override
  @JsonKey()
  final int postCount;

  @override
  String toString() {
    return 'User(id: $id, username: $username, displayName: $displayName, firstName: $firstName, lastName: $lastName, bio: $bio, pronouns: $pronouns, avatarMediaId: $avatarMediaId, coverMediaId: $coverMediaId, location: $location, profession: $profession, website: $website, isVerified: $isVerified, followerCount: $followerCount, followingCount: $followingCount, friendCount: $friendCount, postCount: $postCount)';
  }

  @override
  bool operator ==(Object other) {
    return identical(this, other) ||
        (other.runtimeType == runtimeType &&
            other is _$UserImpl &&
            (identical(other.id, id) || other.id == id) &&
            (identical(other.username, username) ||
                other.username == username) &&
            (identical(other.displayName, displayName) ||
                other.displayName == displayName) &&
            (identical(other.firstName, firstName) ||
                other.firstName == firstName) &&
            (identical(other.lastName, lastName) ||
                other.lastName == lastName) &&
            (identical(other.bio, bio) || other.bio == bio) &&
            (identical(other.pronouns, pronouns) ||
                other.pronouns == pronouns) &&
            (identical(other.avatarMediaId, avatarMediaId) ||
                other.avatarMediaId == avatarMediaId) &&
            (identical(other.coverMediaId, coverMediaId) ||
                other.coverMediaId == coverMediaId) &&
            (identical(other.location, location) ||
                other.location == location) &&
            (identical(other.profession, profession) ||
                other.profession == profession) &&
            (identical(other.website, website) || other.website == website) &&
            (identical(other.isVerified, isVerified) ||
                other.isVerified == isVerified) &&
            (identical(other.followerCount, followerCount) ||
                other.followerCount == followerCount) &&
            (identical(other.followingCount, followingCount) ||
                other.followingCount == followingCount) &&
            (identical(other.friendCount, friendCount) ||
                other.friendCount == friendCount) &&
            (identical(other.postCount, postCount) ||
                other.postCount == postCount));
  }

  @override
  int get hashCode => Object.hash(
    runtimeType,
    id,
    username,
    displayName,
    firstName,
    lastName,
    bio,
    pronouns,
    avatarMediaId,
    coverMediaId,
    location,
    profession,
    website,
    isVerified,
    followerCount,
    followingCount,
    friendCount,
    postCount,
  );

  /// Create a copy of User
  /// with the given fields replaced by the non-null parameter values.
  @JsonKey(includeFromJson: false, includeToJson: false)
  @override
  @pragma('vm:prefer-inline')
  _$$UserImplCopyWith<_$UserImpl> get copyWith =>
      __$$UserImplCopyWithImpl<_$UserImpl>(this, _$identity);
}

abstract class _User extends User {
  const factory _User({
    final String id,
    final String username,
    final String displayName,
    final String firstName,
    final String lastName,
    final String? bio,
    final String? pronouns,
    final String? avatarMediaId,
    final String? coverMediaId,
    final String? location,
    final String? profession,
    final String? website,
    final bool isVerified,
    final int followerCount,
    final int followingCount,
    final int friendCount,
    final int postCount,
  }) = _$UserImpl;
  const _User._() : super._();

  @override
  String get id;
  @override
  String get username;
  @override
  String get displayName;
  @override
  String get firstName;
  @override
  String get lastName;
  @override
  String? get bio;
  @override
  String? get pronouns;
  @override
  String? get avatarMediaId;
  @override
  String? get coverMediaId;
  @override
  String? get location;
  @override
  String? get profession;
  @override
  String? get website;
  @override
  bool get isVerified;
  @override
  int get followerCount;
  @override
  int get followingCount;
  @override
  int get friendCount;
  @override
  int get postCount;

  /// Create a copy of User
  /// with the given fields replaced by the non-null parameter values.
  @override
  @JsonKey(includeFromJson: false, includeToJson: false)
  _$$UserImplCopyWith<_$UserImpl> get copyWith =>
      throw _privateConstructorUsedError;
}
