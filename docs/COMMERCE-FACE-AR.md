# `:core:facear` — virtual try-on

Face AR on the Banuba SDK, behind the same licence token the reel studio uses.
This module owns the licence gate, the try-on domain, the effect sources and the
camera surface. It owns nothing about products — `:feature:commerce` supplies
those and hosts the screen.

## The state of it today: everything works except the effect itself

**No try-on renders yet, and the missing piece is a Banuba effect file. Every
layer on either side of it is proven on a real handset.**

| Layer | State, as observed on a moto g35 (2026-09-13) |
| --- | --- |
| Licence, Face AR included | Working — `FaceArGate: Face AR licence valid=true` |
| Server descriptor | Working — `try_on` nested in the product, four shades |
| Bundle resolution | Working — manifest found, load path resolved |
| Camera and surface | Working — front camera, 1080x1920, stays up |
| Shade selection, compare, Add to bag | Working |
| **An effect that draws lipstick** | **Blocked on a file only Banuba can supply** |

### Why composing Banuba's components does not work

This file previously claimed a makeup try-on shipped and was "composition, not
authoring". That was wrong, and the device proved it in two stages.

First, an effect-root `config.json` is not shaped like a prefab's. Modelling one
on the other is refused outright:

```
Malformed config.json: missing the required property 'scene'.
You may be trying to load an effect for SDK v0.x which is not compatible with
the SDK v1.x
```

The real shape — read from `BeautyBGEffects`, a working effect pulled off the
handset out of Banuba's own demo app, which is the only documentation of this
format that exists — needs `"scene": "<Name>"` and a `"script"` OBJECT
(`{"entry_point": …, "type": "latest"}`), and it declares its ENTIRE scene
inline: assets, components, entities, hierarchy, layers, render_list,
render_targets, its own camera-copy pass. Several hundred lines.

Second, and decisively: **`depends` on a prefab is NOT honoured at an effect
root.** With `scene` and `script` corrected, the effect loads and activates
cleanly and then fails one layer deeper at runtime:

```
[SceneApi] [momentum_lipstick] the lips prefab is not ready yet:
TypeError: cannot read property 'findParameter' of null
```

`findMaterial("shaders/lipsshine/shiny")` returns null — the prefab's assets
were never brought into the scene. Confirmed in `libbanuba.so`, where `depends`
and `apply_order` are prefab-config keys ("`apply_order` key is mondatory in
prefabs") and there is no effect-level prefab-include key. The prefabs are
internal building blocks for **Banuba Studio**, not a composition API.

So the way forward is a ready-made effect from Banuba (their makeup / virtual
try-on product) or one authored in Banuba Studio. Requested 2026-09-13.

### The trap this left behind, and the guard that catches it

An effect the player LOADS takes over rendering. One that declares a scene NAME
but no scene CONTENT renders an empty scene instead of the camera, and the
handset shows **black** — strictly worse-looking than failing to load, where the
SDK falls through to the raw camera. That is what "the camera is not working"
meant when it was reported.

`EffectManifest.effectDeclaresSceneContent` reads the manifest where the bundle
is resolved and refuses to load one that cannot draw, so the plain camera keeps
working and the screen says so in one line. Never load an effect without that
check, and never "fix" a black preview by loading harder.

### The components really are all present

The inventory below is accurate and still useful — it is what an effect authored
in Banuba Studio would be built from. The `makeup` artifact (1.17.6) ships
**complete, production prefabs**
under `assets/bnb-resources/bnb_prefabs/` — `makeup_lipsshine`,
`makeup_lipstick`, `makeup_eyeshadow`, `makeup_blush`, `makeup_eyeliner`,
`makeup_foundation`, `makeup_contour`, `makeup_highlighter`, `makeup_eyebrows`,
`makeup_eyelashes`, `makeup_concealer` and more, each with its own
`config.json`, `schema.json`, `scripts/index.js`, shaders and textures. The
`lips`, `skin`, `eyes` and `face_tracker` artifacts ship the neural networks
those prefabs sample (`flow/lips_segmentation.tflite.bbin` and friends), and the
`scripting` artifact ships the JS runtime (`bnb_js/prefabs.js`, which exports
`Base` and a `parseColor` that takes `"#RRGGBB"` directly). All of it is library
assets, so all of it is merged into the APK and resolved at runtime with no
resource path configured anywhere.

What ships in `effects/momentum_lipstick/` is a manifest naming a scene and a
script, plus a script that turns Momentum`s `setShade` payload into the three
settings `makeup_lipsshine`s schema requires. It loads and it cannot draw, for
the reason above. It is kept because the script and the wire contract are
correct and reusable the moment a real effect exists.

### What a different KIND of try-on would still need

This is the limitation that is real, and it has not gone away. **There is no
prefab for eyewear, jewellery or a watch.** Those are rigid objects occluded by
and anchored to a tracked face or hand, and they need authored 3D content that
nothing in these artifacts provides:

* a **mesh** of the frame, chain or case (`.bsm2`, or a glTF through the `gltf`
  prefab), modelled to real-world scale;
* **PBR textures** for it, plus an environment map if it is to look like metal;
* an **anchor** — which face landmark or hand joint it rides, with an offset and
  a scale rule that survives different head sizes;
* **occlusion** geometry, or the temples of a pair of glasses draw straight
  through the wearer's head.

That is a 3D-asset job with a modeller in it, per product line, and it is the
honest answer to "why is there a lipstick and not sunglasses". Until such a
bundle exists, those kinds resolve `Missing` and the surface says so. The
`gltf`/`gltf_base` prefabs and `face_tracker` are the pieces that would host it,
so the *code* side is already in place.

## Where a bundled effect goes

```
core/facear/src/main/assets/bnb-resources/effects/<effect_slug>/
├── config.json          ← REQUIRED. Banuba's effect manifest. Its presence is
│                          literally what "this effect is installed" means to
│                          BundledTryOnEffects; a directory without it is
│                          treated as a half-copied bundle and reported missing.
├── scripts/index.js       the effect's script — where the JS method in the
│                          contract below is installed on the global object
└── …                      meshes, textures, LUTs, IF the effect needs any.
                           A makeup effect needs none: see momentum_lipstick.
```

What actually ships:

```
bnb-resources/effects/momentum_lipstick/
├── config.json          {apply_order, depends:["makeup_lipsshine"], script}
└── scripts/index.js     setShade(payload) → {color, finish, coverage}
```

Once a real effect exists, a second makeup look is the same shape with a
different prefab behind it
and a different class required in the script — `makeup_eyeshadow` for a shadow,
`makeup_lipsliner` for a liner. The prefab's own `schema.json` (inside the
`makeup` artifact) states the settings it requires; that schema is the contract,
not anything written here.

`<effect_slug>` must equal the server's `try_on.effect_slug` for the product,
character for character. That string is the only join between a catalogue row
and a file on disk.

`bnb-resources` is **Banuba's** convention, not one invented here:
`BanubaSdkManager.getResourcesBase()` points at it and
`BanubaSdkManager.EFFECTS_RESOURCES_PATH` is `/effects`. Using a different
directory would mean passing extra resource paths to
`BanubaSdkManager.initialize` — a second thing to keep in step, for nothing.

Android merges every module's assets, so a bundle placed here is served exactly
as if it sat in `:app`, while living beside the code that loads it.

An AR Cloud effect is the same layout, unpacked by the vendor's
`ArEffectsResourceManager` into `filesDir/bnb-ar-cloud/effects/<slug>/`, and is
loaded by absolute path. Same `config.json` test.

## The JS contract a bundle must implement

Momentum parameterises a try-on by calling one named JavaScript method inside
the effect — `Effect.callJsMethod(name, argumentsJson)`. The name depends on the
kind, and the argument shape is the same for all of them. Both are built by the
pure function `tryOnJsCall` and are constants in `TryOnJsContract`, so this
document, the tests and the code cannot drift.

| `try_on.kind` | method the bundle must define |
| ------------- | ----------------------------- |
| `eyewear`     | `setFrameColour`              |
| `makeup`      | `setShade`                    |
| `jewellery`   | `setMetal`                    |
| `watch`       | `setStrap`                    |

The single argument is a JSON object:

```json
{ "variant": "v-42", "hex": "#C21F3A", "rgb": [0.761, 0.122, 0.227] }
```

* `variant` — the server's variant id, verbatim. Always present. JSON-escaped,
  because it is wire data reaching a script interpreter.
* `hex` — normalised `#RRGGBB`, upper case. **Absent** when the variant carries
  no colour (a frame size) or when the wire value was not a 6-digit hex. A
  bundle must cope with its absence without throwing; `momentum_lipstick`
  answers it by clearing, which is bare lips rather than a stale shade.
* `rgb` — the same colour as three `0.000`–`1.000` numbers, so no bundle has to
  implement hex parsing in JS. Present exactly when `hex` is.

A bundle that wants to switch on the variant id alone can ignore `hex`/`rgb`
entirely; a bundle that is purely a colour shader can ignore `variant`.
`momentum_lipstick` ignores `rgb` — `parseColor` in `bnb_js/prefabs` takes
`"#RRGGBB"` straight, so the floats are redundant for it.

### How the SDK finds the method — the part that is easy to get wrong

`Effect.callJsMethod(name, json)` resolves `name` **against the global object**,
not against the script's `exports`. That is established two ways, and both
matter because getting it wrong is a silent no-op on a device:

* Banuba's own documented example is
  `effect.callJsMethod("Eyes.color", JSON.stringify(colour))`, a **dotted path**
  through globals, with `effect.evalJs('Eyes.color("…")')` given as the exact
  equivalent;
* the effect player's own script wiring, read out of `libbanuba.so`, builds
  plain global assignments: it evaluates
  `import * as <N> from '<module>'; globalThis.<N> = <N>;` for a script module,
  and instantiates a prefab's class as
  `bnb_…<name> = new (require('<script>').<PascalCaseName>)(…)` — an
  unqualified assignment, i.e. a global — before driving it through
  `.setPrefabSettings(<json>)`.

So a bundle's method must be **installed on `globalThis`**. `exports` alone is
not callable. `momentum_lipstick/scripts/index.js` therefore ends with

```js
globalThis.setShade = setShade
```

and exports the same function as well, for the `require` path.

The other consequence: `callJsMethod` may hand the arguments over as JSON
**text** while `evalJs` hands over an **object literal**. A bundle should accept
both — `momentum_lipstick` does, in `payloadOf`, and a unit test asserts it
still does.

One more, from the same reading: `depends` entries resolve to
`bnb_prefabs/<name>/config.json`, so a dependency can only ever name a prefab
the SDK ships. It is not a path and cannot reach into the effect's own directory.

### The escape hatch

A variant may instead carry its own `js` string — the exact effect-specific
call for a look no colour can drive (a gradient lens, a two-tone strap, a matte
finish). When present it **replaces** the generated call entirely and reaches
`Effect.evalJs` verbatim. That is deliberate: it exists because the generated
call could not express the look, so rewriting it would defeat it. It is
seller-set catalogue content running in the effect's own sandboxed interpreter
against a local camera frame — no app API, no network, no filesystem — and it
must stay a catalogue field rather than anything a buyer can type.

### Which resource path the SDK is given

**None.** `BanubaSdkManager.initialize(context, token)` is called with no
resource-path vararg, because that vararg exists to *add* asset roots and the
SDK's own default is already the right one: `getResourcesBase()` returns
`"bnb-resources"` and `EFFECTS_RESOURCES_PATH` is `"/effects"`, so
`assets/bnb-resources/effects/<slug>` is found with no configuration and is
loaded by the relative path `effects/<slug>`. Passing a root of our own would
be a second place for the layout to be stated, and the two would eventually
disagree.

## What must be true before a face appears on screen

In order, all of it:

1. **A real licence covering Face AR.** The trial token in `.secrets/banuba.token`
   must include the Face AR product line, not only the Video Editor. A token
   that covers only the editor leaves the gate at `Invalid`, and the product
   screen then shows the one-line licence notice.
2. **An effect bundle for the product's slug**, laid out as above, with a
   `config.json`. Satisfied for `momentum_lipstick` and for nothing else.
3. **The kind's method installed on `globalThis` by that bundle**, or the effect
   loads and the shade strip does nothing. Satisfied for `setShade`.
4. **A server sending `try_on`** on the product payload with `capable: true`,
   `kind: "makeup"`, `effect_slug: "momentum_lipstick"`, and variants carrying
   six-digit hexes. A variant with no hex loads the effect and clears it.
5. **A real device.** None of the camera path is unit-testable and none of it is
   device-verified yet — see the one-process warning below. **Rendering is the
   one link in this chain that nothing in this repository can prove**: the files
   are checked in, the resolver finds them and the build packages them, but that
   a lipstick appears, in the right colour, at an acceptable frame rate, is only
   knowable from a handset.

### First run: what to look for, and what failure looks like

Filter logcat on `FaceArSession` for this module's own side, and on
`BanubaSdkManager` / `bnb` for the vendor's. Then, in order:

* **A licence notice instead of a camera** — the gate is at `Invalid`; the token
  does not cover Face AR. Nothing to do with this bundle.
* **"This build carries no try-on effect for this product yet"** — the server's
  `effect_slug` is not `momentum_lipstick` character for character. Check the
  descriptor, not the assets.
* **A camera with a bare face, no error** — the effect loaded but `setShade`
  never ran or never found its prefab. Look for
  `[momentum_lipstick] the lips prefab is not ready yet` (the scene was not up
  when the shade was applied — tap a second swatch and see whether it appears)
  or `has no '…' state property` (a settings key got misspelled, which would
  mean the schema changed under us).
* **A camera with a bare face and no `[momentum_lipstick]` line at all** — the
  script was never loaded, or `callJsMethod` did not resolve `setShade`. The
  one-line fallback is `evalJs`: `TryOnJsCall.Method.script` is already the same
  call as source, so `FaceArSession.apply` can send `call.script` to `evalJs`
  instead and the JS side needs no change.
* **A lipstick in the wrong colour** — `parseColor` read the hex differently
  than intended; the payload's `rgb` floats are the cross-check.
* **A dropped frame rate** — `makeup_base` runs in `"speed"` mode by default
  and this bundle does not change it; it also pulls the `lips` and
  `lips_shining` segmentation networks, which is real per-frame work on a
  low-end device.
* **A `config.json` or `.js` that cannot be opened at all** — assets are stored
  compressed and nothing here sets `noCompress`. Checked in the built APK: this
  bundle's two files are `Defl:N`, and so are Banuba's own
  `bnb_prefabs/*/config.json`, so the bundle gets exactly the treatment the
  SDK's own working assets get. If a device somehow says otherwise,
  `androidResources { noCompress += listOf(".json", ".js") }` in this module is
  the fix.

## Two Banuba product lines in one process

Two of them live in this app on one licence token:

* the reel studio starts the **Video Editor** — `EditorSdk.initialize(token)`
  plus the vendor's Koin graph, in `:feature:post`;
* try-on starts **Face AR** — `BanubaSdkManager.initialize(context, token)`,
  no Koin, here.

**The licence side of that is settled and low risk**, from disassembling both
paths. `EditorSdk.initialize` touches only core-sdk's own licence manager.
`BanubaSdkManager.initialize` returns immediately if it has already run, then
does exactly three things — `ReLinker.loadLibrary("banuba")` (cached),
`ContextProvider.setContext`, and `UtilityManager.initialize(paths, token)`. So
both in one process is benign, and `FaceArGate` is deliberately plain about it:
one single-init latch, a licence read instead of a second initialise, and
`deinitialize()` never called on any path (it would pull the native library out
from under a live reel export).

**The hazard that is real is the camera and the GL surface.** The video
editor's Koin graph creates its own effect player and a try-on screen creates
another; two players contending for the front camera present as a **black
preview or a crash, never a licence error.** What prevents it is structural, in
`FaceArSession`: the camera is closed and the player paused on every lifecycle
stop, not only on dispose, and nothing opens a camera while the composable is
not resumed.

**Device check, in both orders:** open the reel camera, back out, open a
product try-on — then try-on first and the reel camera second.
