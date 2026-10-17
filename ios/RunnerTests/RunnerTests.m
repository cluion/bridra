@import XCTest;
@import Flutter;
#import "FLTIntegrationTestRunner.h"

@interface RunnerTests : XCTestCase
@end

@implementation RunnerTests

- (void)testHTTPRoundTrip {
  FlutterViewController *controller = nil;
  for (UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
    if (![scene isKindOfClass:UIWindowScene.class]) continue;
    for (UIWindow *window in ((UIWindowScene *)scene).windows) {
      if ([window.rootViewController isKindOfClass:FlutterViewController.class]) {
        controller = (FlutterViewController *)window.rootViewController;
      }
    }
  }
  XCTAssertNotNil(controller);
  // Initialize native accessibility before Flutter records its per-test handle baseline.
  [controller.engine ensureSemanticsEnabled];
  FlutterMethodChannel *channel = [FlutterMethodChannel methodChannelWithName:@"bridra.test.xctest"
                                                           binaryMessenger:controller.engine.binaryMessenger];
  [channel invokeMethod:@"ready" arguments:nil result:^(id result) {
    XCTAssertNil(result);
  }];
  NSMutableArray<NSString *> *completedTests = [NSMutableArray array];
  [[FLTIntegrationTestRunner new] testIntegrationTestWithResults:^(SEL selector, BOOL success, NSString *message) {
    [completedTests addObject:NSStringFromSelector(selector)];
    XCTAssertTrue(success, @"%@", message ?: @"Dart integration test failed");
  }];
  XCTAssertTrue([completedTests isEqualToArray:@[@"testPlatformCompletesHttpSmokeFlow"]] ||
                [completedTests isEqualToArray:@[@"testIntentionalDartAssertionFailure"]]);
}

@end
