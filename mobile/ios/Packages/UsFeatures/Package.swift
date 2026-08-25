// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "UsFeatures",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(name: "FeatureFeed", targets: ["FeatureFeed"]),
        .library(name: "FeatureReels", targets: ["FeatureReels"]),
        .library(name: "FeaturePost", targets: ["FeaturePost"]),
        .library(name: "FeatureAuth", targets: ["FeatureAuth"]),
        .library(name: "FeatureProfile", targets: ["FeatureProfile"]),
        .library(name: "FeatureExplore", targets: ["FeatureExplore"]),
        .library(name: "FeatureChat", targets: ["FeatureChat"]),
        .library(name: "FeatureCreate", targets: ["FeatureCreate"]),
        .library(name: "FeatureStory", targets: ["FeatureStory"]),
        .library(name: "FeatureNotifications", targets: ["FeatureNotifications"]),
        .library(name: "FeatureSettings", targets: ["FeatureSettings"]),
        .library(name: "FeatureWatch", targets: ["FeatureWatch"]),
        .library(name: "FeatureLive", targets: ["FeatureLive"]),
        .library(name: "FeatureAudio", targets: ["FeatureAudio"]),
        .library(name: "FeatureWallet", targets: ["FeatureWallet"]),
        .library(name: "FeatureCommerce", targets: ["FeatureCommerce"]),
        .library(name: "FeatureDating", targets: ["FeatureDating"]),
        .library(name: "FeatureServicesHub", targets: ["FeatureServicesHub"]),
        .library(name: "FeatureFood", targets: ["FeatureFood"]),
        .library(name: "FeatureRides", targets: ["FeatureRides"]),
        .library(name: "FeatureBillPay", targets: ["FeatureBillPay"]),
        .library(name: "FeatureCreatorStudio", targets: ["FeatureCreatorStudio"]),
        .library(name: "FeatureCommunities", targets: ["FeatureCommunities"]),
        .library(name: "FeatureCalling", targets: ["FeatureCalling"]),
        .library(name: "FeatureSafety", targets: ["FeatureSafety"]),
        .library(name: "FeatureMemories", targets: ["FeatureMemories"]),
        .library(name: "FeatureAI", targets: ["FeatureAI"]),
        .library(name: "FeatureQA", targets: ["FeatureQA"]),
        .library(name: "FeatureAudioSpaces", targets: ["FeatureAudioSpaces"]),
        .library(name: "FeatureMediaEditor", targets: ["FeatureMediaEditor"]),
        .library(name: "FeatureQRCode", targets: ["FeatureQRCode"]),
        .library(name: "FeatureRewards", targets: ["FeatureRewards"]),
        .library(name: "FeatureLiveCommerce", targets: ["FeatureLiveCommerce"]),
        .library(name: "FeatureBroadcastChannels", targets: ["FeatureBroadcastChannels"]),
        .library(name: "FeatureSlambook", targets: ["FeatureSlambook"]),
        .library(name: "FeatureSecurity", targets: ["FeatureSecurity"]),
        .library(name: "FeatureSplitBill", targets: ["FeatureSplitBill"]),
        .library(name: "FeatureSubscriptions", targets: ["FeatureSubscriptions"]),
        .library(name: "FeatureRadar", targets: ["FeatureRadar"]),
        .library(name: "FeatureARCamera", targets: ["FeatureARCamera"]),
        .library(name: "FeatureEvents", targets: ["FeatureEvents"]),
        .library(name: "FeatureGifting", targets: ["FeatureGifting"]),
        .library(name: "FeatureStoryStickers", targets: ["FeatureStoryStickers"]),
        .library(name: "FeatureMultiLive", targets: ["FeatureMultiLive"]),
        .library(name: "FeatureVideoScrubber", targets: ["FeatureVideoScrubber"]),
        .library(name: "FeatureSoundboard", targets: ["FeatureSoundboard"]),
        .library(name: "FeatureThemes", targets: ["FeatureThemes"]),
        .library(name: "FeatureBadges", targets: ["FeatureBadges"]),
        .library(name: "FeaturePIP", targets: ["FeaturePIP"]),
        .library(name: "FeatureRemix", targets: ["FeatureRemix"]),
        .library(name: "FeatureMiniGames", targets: ["FeatureMiniGames"]),
        .library(name: "FeatureCollabs", targets: ["FeatureCollabs"]),
        .library(name: "FeatureLiveLocation", targets: ["FeatureLiveLocation"]),
        .library(name: "FeatureMusicStickers", targets: ["FeatureMusicStickers"]),
        .library(name: "FeatureDisappearingMedia", targets: ["FeatureDisappearingMedia"]),
        .library(name: "FeatureAccountSwitcher", targets: ["FeatureAccountSwitcher"]),
        .library(name: "FeatureTipJar", targets: ["FeatureTipJar"]),
        .library(name: "FeatureAppShortcuts", targets: ["FeatureAppShortcuts"]),
        .library(name: "FeaturePolls", targets: ["FeaturePolls"]),
        .library(name: "FeatureStage", targets: ["FeatureStage"]),
        .library(name: "FeatureClassifieds", targets: ["FeatureClassifieds"]),
        .library(name: "FeatureDigitalGold", targets: ["FeatureDigitalGold"]),
        .library(name: "FeatureTranslator", targets: ["FeatureTranslator"]),
        .library(name: "FeatureAMA", targets: ["FeatureAMA"]),
        .library(name: "FeatureStorefront", targets: ["FeatureStorefront"]),
        .library(name: "FeatureCloseFriends", targets: ["FeatureCloseFriends"]),
        .library(name: "FeatureEncryption", targets: ["FeatureEncryption"]),
        .library(name: "FeatureTrivia", targets: ["FeatureTrivia"]),
        .library(name: "FeatureCountdown", targets: ["FeatureCountdown"]),
        .library(name: "FeatureVoiceFX", targets: ["FeatureVoiceFX"]),
        .library(name: "FeatureMetro", targets: ["FeatureMetro"]),
        .library(name: "FeatureSettlements", targets: ["FeatureSettlements"]),
        .library(name: "FeatureChatPolls", targets: ["FeatureChatPolls"]),
        .library(name: "FeatureStoryQuiz", targets: ["FeatureStoryQuiz"]),
        .library(name: "FeatureOfflineVault", targets: ["FeatureOfflineVault"]),
        .library(name: "FeatureParking", targets: ["FeatureParking"]),
        .library(name: "FeatureBrandCollabs", targets: ["FeatureBrandCollabs"]),
        .library(name: "FeatureWatchParty", targets: ["FeatureWatchParty"]),
        .library(name: "FeatureMentions", targets: ["FeatureMentions"]),
        .library(name: "FeatureScheduledDMs", targets: ["FeatureScheduledDMs"]),
        .library(name: "FeaturePharmacy", targets: ["FeaturePharmacy"]),
        .library(name: "FeatureCourses", targets: ["FeatureCourses"]),
        .library(name: "FeatureChatHuddles", targets: ["FeatureChatHuddles"]),
        .library(name: "FeatureLinkSticker", targets: ["FeatureLinkSticker"]),
        .library(name: "FeatureSecretChat", targets: ["FeatureSecretChat"]),
        .library(name: "FeatureEVCharging", targets: ["FeatureEVCharging"]),
        .library(name: "FeatureMembershipClub", targets: ["FeatureMembershipClub"]),
        .library(name: "FeatureGroupExpenses", targets: ["FeatureGroupExpenses"]),
        .library(name: "FeatureAddYours", targets: ["FeatureAddYours"]),
        .library(name: "FeatureCampus", targets: ["FeatureCampus"]),
        .library(name: "FeatureLaundry", targets: ["FeatureLaundry"]),
        .library(name: "FeaturePodcasts", targets: ["FeaturePodcasts"]),
        .library(name: "FeatureSharedMedia", targets: ["FeatureSharedMedia"]),
        .library(name: "FeatureReactionSlider", targets: ["FeatureReactionSlider"]),
        .library(name: "FeatureFanClubs", targets: ["FeatureFanClubs"]),
        .library(name: "FeatureFitness", targets: ["FeatureFitness"]),
        .library(name: "FeatureHomeServices", targets: ["FeatureHomeServices"]),
        .library(name: "FeatureVoiceTranscripts", targets: ["FeatureVoiceTranscripts"]),
        .library(name: "FeatureAvatarReactions", targets: ["FeatureAvatarReactions"]),
        .library(name: "FeaturePetCare", targets: ["FeaturePetCare"]),
        .library(name: "FeatureFanLeaderboard", targets: ["FeatureFanLeaderboard"]),
        .library(name: "FeatureBookExchange", targets: ["FeatureBookExchange"]),
        .library(name: "FeatureChatGames", targets: ["FeatureChatGames"]),
        .library(name: "FeatureLiveActivities", targets: ["FeatureLiveActivities"]),
        .library(name: "FeatureWidgets", targets: ["FeatureWidgets"]),
        .library(name: "FeatureNFC", targets: ["FeatureNFC"]),
        .library(name: "FeatureSharePlay", targets: ["FeatureSharePlay"]),
        .library(name: "FeatureAppIntents", targets: ["FeatureAppIntents"]),
        .library(name: "FeatureCarPlay", targets: ["FeatureCarPlay"]),
        .library(name: "FeatureWatchCompanion", targets: ["FeatureWatchCompanion"]),
        .library(name: "FeatureSports", targets: ["FeatureSports"]),
        .library(name: "FeatureMediaKit", targets: ["FeatureMediaKit"]),
        .library(name: "FeatureStoryGame", targets: ["FeatureStoryGame"]),
        .library(name: "FeatureWeatherSticker", targets: ["FeatureWeatherSticker"]),
        .library(name: "FeatureMovies", targets: ["FeatureMovies"]),
        .library(name: "FeatureCarpool", targets: ["FeatureCarpool"]),
        .library(name: "FeatureCollectibles", targets: ["FeatureCollectibles"]),
        .library(name: "FeatureLyrics", targets: ["FeatureLyrics"]),
        .library(name: "FeatureStoryHeatmap", targets: ["FeatureStoryHeatmap"]),
        .library(name: "FeatureCoworking", targets: ["FeatureCoworking"]),
        .library(name: "FeatureAudioClipper", targets: ["FeatureAudioClipper"]),
        .library(name: "FeatureTransitStatus", targets: ["FeatureTransitStatus"]),
        .library(name: "FeatureConfessions", targets: ["FeatureConfessions"]),
    ],
    dependencies: [
        .package(path: "../UsCore"),
    ],
    targets: [
        .target(
            name: "FeatureFeed",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureFeed"
        ),
        .target(
            name: "FeatureReels",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
                "FeatureFeed",
            ],
            path: "Sources/FeatureReels"
        ),
        .target(
            name: "FeaturePost",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeaturePost"
        ),
        .target(
            name: "FeatureAuth",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureAuth"
        ),
        .target(
            name: "FeatureProfile",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
                "FeatureSettings",
                "FeatureQRCode",
            ],
            path: "Sources/FeatureProfile"
        ),
        .target(
            name: "FeatureExplore",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureExplore"
        ),
        .target(
            name: "FeatureChat",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureChat"
        ),
        .target(
            name: "FeatureCreate",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
                "FeatureAudio",
            ],
            path: "Sources/FeatureCreate"
        ),
        .target(
            name: "FeatureStory",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureStory"
        ),
        .target(
            name: "FeatureNotifications",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureNotifications"
        ),
        .target(
            name: "FeatureSettings",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSettings"
        ),
        .target(
            name: "FeatureWatch",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureWatch"
        ),
        .target(
            name: "FeatureLive",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureLive"
        ),
        .target(
            name: "FeatureAudio",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureAudio"
        ),
        .target(
            name: "FeatureWallet",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureWallet"
        ),
        .target(
            name: "FeatureCommerce",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCommerce"
        ),
        .target(
            name: "FeatureDating",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureDating"
        ),
        .target(
            name: "FeatureServicesHub",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureServicesHub"
        ),
        .target(
            name: "FeatureFood",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureFood"
        ),
        .target(
            name: "FeatureRides",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureRides"
        ),
        .target(
            name: "FeatureBillPay",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureBillPay"
        ),
        .target(
            name: "FeatureCreatorStudio",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCreatorStudio"
        ),
        .target(
            name: "FeatureCommunities",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCommunities"
        ),
        .target(
            name: "FeatureCalling",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCalling"
        ),
        .target(
            name: "FeatureSafety",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSafety"
        ),
        .target(
            name: "FeatureMemories",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureMemories"
        ),
        .target(
            name: "FeatureAI",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureAI"
        ),
        .target(
            name: "FeatureQA",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureQA"
        ),
        .target(
            name: "FeatureAudioSpaces",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureAudioSpaces"
        ),
        .target(
            name: "FeatureMediaEditor",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMediaEditor"
        ),
        .target(
            name: "FeatureQRCode",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureQRCode"
        ),
        .target(
            name: "FeatureRewards",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureRewards"
        ),
        .target(
            name: "FeatureLiveCommerce",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureLiveCommerce"
        ),
        .target(
            name: "FeatureBroadcastChannels",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureBroadcastChannels"
        ),
        .target(
            name: "FeatureSlambook",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureSlambook"
        ),
        .target(
            name: "FeatureSecurity",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureSecurity"
        ),
        .target(
            name: "FeatureSplitBill",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSplitBill"
        ),
        .target(
            name: "FeatureSubscriptions",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSubscriptions"
        ),
        .target(
            name: "FeatureRadar",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureRadar"
        ),
        .target(
            name: "FeatureARCamera",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureARCamera"
        ),
        .target(
            name: "FeatureEvents",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureEvents"
        ),
        .target(
            name: "FeatureGifting",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureGifting"
        ),
        .target(
            name: "FeatureStoryStickers",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureStoryStickers"
        ),
        .target(
            name: "FeatureMultiLive",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureMultiLive"
        ),
        .target(
            name: "FeatureVideoScrubber",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureVideoScrubber"
        ),
        .target(
            name: "FeatureSoundboard",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSoundboard"
        ),
        .target(
            name: "FeatureThemes",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureThemes"
        ),
        .target(
            name: "FeatureBadges",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureBadges"
        ),
        .target(
            name: "FeaturePIP",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeaturePIP"
        ),
        .target(
            name: "FeatureRemix",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureRemix"
        ),
        .target(
            name: "FeatureMiniGames",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMiniGames"
        ),
        .target(
            name: "FeatureCollabs",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCollabs"
        ),
        .target(
            name: "FeatureLiveLocation",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureLiveLocation"
        ),
        .target(
            name: "FeatureMusicStickers",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMusicStickers"
        ),
        .target(
            name: "FeatureDisappearingMedia",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureDisappearingMedia"
        ),
        .target(
            name: "FeatureAccountSwitcher",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureAccountSwitcher"
        ),
        .target(
            name: "FeatureTipJar",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureTipJar"
        ),
        .target(
            name: "FeatureAppShortcuts",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureAppShortcuts"
        ),
        .target(
            name: "FeaturePolls",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeaturePolls"
        ),
        .target(
            name: "FeatureStage",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureStage"
        ),
        .target(
            name: "FeatureClassifieds",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureClassifieds"
        ),
        .target(
            name: "FeatureDigitalGold",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureDigitalGold"
        ),
        .target(
            name: "FeatureTranslator",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureTranslator"
        ),
        .target(
            name: "FeatureAMA",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureAMA"
        ),
        .target(
            name: "FeatureStorefront",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureStorefront"
        ),
        .target(
            name: "FeatureCloseFriends",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCloseFriends"
        ),
        .target(
            name: "FeatureEncryption",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureEncryption"
        ),
        .target(
            name: "FeatureTrivia",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureTrivia"
        ),
        .target(
            name: "FeatureCountdown",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCountdown"
        ),
        .target(
            name: "FeatureVoiceFX",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureVoiceFX"
        ),
        .target(
            name: "FeatureMetro",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureMetro"
        ),
        .target(
            name: "FeatureSettlements",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSettlements"
        ),
        .target(
            name: "FeatureChatPolls",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureChatPolls"
        ),
        .target(
            name: "FeatureStoryQuiz",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureStoryQuiz"
        ),
        .target(
            name: "FeatureOfflineVault",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureOfflineVault"
        ),
        .target(
            name: "FeatureParking",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureParking"
        ),
        .target(
            name: "FeatureBrandCollabs",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureBrandCollabs"
        ),
        .target(
            name: "FeatureWatchParty",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureWatchParty"
        ),
        .target(
            name: "FeatureMentions",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMentions"
        ),
        .target(
            name: "FeatureScheduledDMs",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureScheduledDMs"
        ),
        .target(
            name: "FeaturePharmacy",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeaturePharmacy"
        ),
        .target(
            name: "FeatureCourses",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCourses"
        ),
        .target(
            name: "FeatureChatHuddles",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureChatHuddles"
        ),
        .target(
            name: "FeatureLinkSticker",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureLinkSticker"
        ),
        .target(
            name: "FeatureSecretChat",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureSecretChat"
        ),
        .target(
            name: "FeatureEVCharging",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureEVCharging"
        ),
        .target(
            name: "FeatureMembershipClub",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMembershipClub"
        ),
        .target(
            name: "FeatureGroupExpenses",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureGroupExpenses"
        ),
        .target(
            name: "FeatureAddYours",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureAddYours"
        ),
        .target(
            name: "FeatureCampus",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCampus"
        ),
        .target(
            name: "FeatureLaundry",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureLaundry"
        ),
        .target(
            name: "FeaturePodcasts",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeaturePodcasts"
        ),
        .target(
            name: "FeatureSharedMedia",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureSharedMedia"
        ),
        .target(
            name: "FeatureReactionSlider",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureReactionSlider"
        ),
        .target(
            name: "FeatureFanClubs",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureFanClubs"
        ),
        .target(
            name: "FeatureFitness",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureFitness"
        ),
        .target(
            name: "FeatureHomeServices",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureHomeServices"
        ),
        .target(
            name: "FeatureVoiceTranscripts",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureVoiceTranscripts"
        ),
        .target(
            name: "FeatureAvatarReactions",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureAvatarReactions"
        ),
        .target(
            name: "FeaturePetCare",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeaturePetCare"
        ),
        .target(
            name: "FeatureFanLeaderboard",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureFanLeaderboard"
        ),
        .target(
            name: "FeatureBookExchange",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureBookExchange"
        ),
        .target(
            name: "FeatureChatGames",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureChatGames"
        ),
        .target(
            name: "FeatureLiveActivities",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureLiveActivities"
        ),
        .target(
            name: "FeatureWidgets",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureWidgets"
        ),
        .target(
            name: "FeatureNFC",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureNFC"
        ),
        .target(
            name: "FeatureSharePlay",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureSharePlay"
        ),
        .target(
            name: "FeatureAppIntents",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureAppIntents"
        ),
        .target(
            name: "FeatureCarPlay",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCarPlay"
        ),
        .target(
            name: "FeatureWatchCompanion",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureWatchCompanion"
        ),
        .target(
            name: "FeatureSports",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureSports"
        ),
        .target(
            name: "FeatureMediaKit",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureMediaKit"
        ),
        .target(
            name: "FeatureStoryGame",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureStoryGame"
        ),
        .target(
            name: "FeatureWeatherSticker",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureWeatherSticker"
        ),
        .target(
            name: "FeatureMovies",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureMovies"
        ),
        .target(
            name: "FeatureCarpool",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCarpool"
        ),
        .target(
            name: "FeatureCollectibles",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureCollectibles"
        ),
        .target(
            name: "FeatureLyrics",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureLyrics"
        ),
        .target(
            name: "FeatureStoryHeatmap",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureStoryHeatmap"
        ),
        .target(
            name: "FeatureCoworking",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureCoworking"
        ),
        .target(
            name: "FeatureAudioClipper",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Sources/FeatureAudioClipper"
        ),
        .target(
            name: "FeatureTransitStatus",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
                .product(name: "UsNetwork", package: "UsCore"),
            ],
            path: "Sources/FeatureTransitStatus"
        ),
        .target(
            name: "FeatureConfessions",
            dependencies: [
                .product(name: "UsModel", package: "UsCore"),
                .product(name: "UsDesignSystem", package: "UsCore"),
            ],
            path: "Sources/FeatureConfessions"
        ),
        .testTarget(
            name: "FeatureTests",
            dependencies: [
                "FeatureFeed",
                "FeatureStory",
                "FeatureChat",
                "FeatureReels",
                "FeatureProfile",
                "FeatureAudio",
                "FeatureWallet",
                "FeatureCommerce",
                "FeatureDating",
                "FeatureFood",
                "FeatureRides",
                "FeatureBillPay",
                "FeatureCreatorStudio",
                "FeatureCommunities",
                "FeatureAI",
                "FeatureQA",
                "FeatureAudioSpaces",
                "FeatureRewards",
                "FeatureEvents",
                "FeatureMultiLive",
                "FeatureMovies",
                "FeatureCarpool",
                "FeatureCoworking",
                "FeatureTransitStatus",
                "FeaturePetCare",
                "FeatureSports",
                "FeatureHomeServices",
                "FeatureCreate",
                "FeatureNotifications",
                "FeatureAuth",
                .product(name: "UsMedia", package: "UsCore"),
            ],
            path: "Tests/FeatureTests"
        ),
    ]
)
