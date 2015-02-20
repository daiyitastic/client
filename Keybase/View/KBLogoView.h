//
//  KBLogoView.h
//  Keybase
//
//  Created by Gabriel on 1/14/15.
//  Copyright (c) 2015 Gabriel Handford. All rights reserved.
//

#import <Foundation/Foundation.h>

#import "KBAppKit.h"

@interface KBLogoView : YONSView <KBNavigationTitleView>

@property KBButton *backView;
@property (nonatomic) NSString *title;
@property (nonatomic, getter=isProgressEnabled) BOOL progressEnabled;

@end
