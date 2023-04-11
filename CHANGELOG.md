# Changelog

## [1.2.2](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v1.2.1...v1.2.2) (2023-04-11)


### Bug Fixes

* update dependencies to latest versions ([#277](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/277)) ([e263db1](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/e263db1c5d53beda6df5a91faa1ef0cb085cb50b))

## [1.2.1](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v1.2.0...v1.2.1) (2023-03-14)


### Bug Fixes

* update dependencies to latest versions ([#247](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/247)) ([5c5b680](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/5c5b68029e2bdb6f41faeedf35e970f3ca316636))

## [1.2.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v1.1.0...v1.2.0) (2023-02-15)


### Features

* add support for Go 1.20 ([#216](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/216)) ([43e16c0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/43e16c049b7e2d55c73ee2a21ef936f18620923f))


### Bug Fixes

* improve reliability of certificate refresh ([#220](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/220)) ([db686a9](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/db686a9058b7998472a3a32df6598c90390abf84))
* prevent repeated context expired errors ([#228](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/228)) ([33d1369](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/33d1369f4ce7011b15e91004caddc350a64d2127))

## [1.1.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v1.0.0...v1.1.0) (2023-01-10)


### Features

* use handshake context when possible ([#199](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/199)) ([533eb4e](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/533eb4e3cce97ac5f3fbfa3c0c7cd4f2e857ff05))

## [1.0.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.4.0...v1.0.0) (2022-12-13)


### Miscellaneous Chores

* release 1.0.0 ([#188](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/188)) ([34c9c5b](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/34c9c5b70be51ef8dc3a25ce92f730cc002b1571))

## [0.4.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.3.1...v0.4.0) (2022-11-28)


### Features

* limit ephemeral certificates to 1 hour ([#168](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/168)) ([b9bb918](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/b9bb918a1a9befb44c4a0cfce5e7a48a80e3ea20))

## [0.3.1](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.3.0...v0.3.1) (2022-11-01)


### Bug Fixes

* update dependencies to latest versions ([#150](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/150)) ([369121b](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/369121b7421243c2be6f2fa3e6c998a8d01d08e2))

## [0.3.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.2.2...v0.3.0) (2022-10-18)


### Features

* add support for Go 1.19 ([#123](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/123)) ([8e93b9f](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/8e93b9fd5ad508b4f30eb62ccedfcf326d34e03d))

## [0.2.2](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.2.1...v0.2.2) (2022-09-07)


### Bug Fixes

* support shorter refresh durations ([#103](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/103)) ([6f6a7a0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/6f6a7a05875c3d62a8a71cd54c59db8d793d3c25))

## [0.2.1](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.2.0...v0.2.1) (2022-08-01)


### Bug Fixes

* include intermediate cert when verifying server ([#83](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/83)) ([072c20d](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/072c20d974ac6705617f10cd8f3889a4adc685ee))

## [0.2.0](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.1.2...v0.2.0) (2022-07-12)


### ⚠ BREAKING CHANGES

* use instance uri instead of conn name (#15)

### Features

* add AlloyDB instance type ([da23ca9](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/da23ca9579f5b90e86287e5b7dc689a549ea9240))
* add AlloyDB refresher ([c3a4372](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/c3a43727a1b1d76ce50c288155fa8c6bb31d09ab))
* add AlloyDB refresher ([#2](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/2)) ([d0d6a11](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/d0d6a119fcb3cc5613de065a168f415dbce70789))
* add support for dialer ([#4](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/4)) ([483ffda](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/483ffdae1870835db79aa04c59a6322b9ec8e9bb))
* add WithUserAgent opt ([#10](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/10)) ([6582164](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/658216477813b92aadfd44403b9389dcaea9f081))
* switch to Connect API and verify server name ([#70](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/70)) ([36197b6](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/36197b6c9f6626952d37e30087d986c4226a13dc))
* switch to prod endpoint ([#13](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/13)) ([b477122](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/b47712202088e43533820c51633dff65fe552ce4))
* use v1beta endpoint ([#16](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/16)) ([bfe5fe5](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/bfe5fe56294c76bf7be4ad1ba09cc7b982479d24))


### Bug Fixes

* adjust alignment for 32-bit arch ([#33](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/33)) ([b0e76fa](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/b0e76fa5384fc66365b5d15b56927942f4031fda))
* admin API client handles non-20x responses ([#14](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/14)) ([c2f5dc9](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/c2f5dc92e1a57262c10cd715fc6082a931d0cf70))
* prevent memory leak in driver ([#22](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/22)) ([861d798](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/861d798e0715f16b88d501950a8d9a0493cc8257))
* specify scope for WithCredentialsFile/JSON ([#29](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/29)) ([9424d57](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/9424d572346f16cee86e80dccc9e01618b97df73))
* update dependencies to latest versions ([#55](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/55)) ([7e3af54](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/7e3af549b4991d77348751b8f1fa9d0074846782))
* use instance uri instead of conn name ([#15](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/15)) ([0da01fd](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/0da01fd311f1e8829be0a9eb0efdeb169ee7c555))


## [0.1.2](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.1.1...v0.1.2) (2022-06-07)


### Bug Fixes

* update dependencies to latest versions ([#55](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/55)) ([7e3af54](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/7e3af549b4991d77348751b8f1fa9d0074846782))

### [0.1.1](https://github.com/GoogleCloudPlatform/alloydb-go-connector/compare/v0.1.0...v0.1.1) (2022-05-18)


### Bug Fixes

* adjust alignment for 32-bit arch ([#33](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/33)) ([b0e76fa](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/b0e76fa5384fc66365b5d15b56927942f4031fda))
* specify scope for WithCredentialsFile/JSON ([#29](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/29)) ([9424d57](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/9424d572346f16cee86e80dccc9e01618b97df73))

## 0.1.0 (2022-04-26)


### ⚠ BREAKING CHANGES

* use instance uri instead of conn name (#15)

### Features

* add AlloyDB refresher ([#2](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/2)) ([d0d6a11](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/d0d6a119fcb3cc5613de065a168f415dbce70789))
* add support for dialer ([#4](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/4)) ([483ffda](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/483ffdae1870835db79aa04c59a6322b9ec8e9bb))
* add WithUserAgent opt ([#10](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/10)) ([6582164](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/658216477813b92aadfd44403b9389dcaea9f081))
* switch to prod endpoint ([#13](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/13)) ([b477122](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/b47712202088e43533820c51633dff65fe552ce4))
* use v1beta endpoint ([#16](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/16)) ([bfe5fe5](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/bfe5fe56294c76bf7be4ad1ba09cc7b982479d24))


### Bug Fixes

* admin API client handles non-20x responses ([#14](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/14)) ([c2f5dc9](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/c2f5dc92e1a57262c10cd715fc6082a931d0cf70))
* prevent memory leak in driver ([#22](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/22)) ([861d798](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/861d798e0715f16b88d501950a8d9a0493cc8257))
* use instance uri instead of conn name ([#15](https://github.com/GoogleCloudPlatform/alloydb-go-connector/issues/15)) ([0da01fd](https://github.com/GoogleCloudPlatform/alloydb-go-connector/commit/0da01fd311f1e8829be0a9eb0efdeb169ee7c555))
