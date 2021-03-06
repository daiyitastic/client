// @flow
import React from 'react'
import {Text} from '../../../common-adapters'
import {globalStyles} from '../../../styles'

import type {Props} from './loading-more'

const MessageLoadingMore = ({style, hasMoreItems}: Props) => (
  <div style={{...globalStyles.flexBoxColumn, alignItems: 'center', ...style, ...(hasMoreItems ? null : {opacity: 0})}}>
    <Text type='BodySmallSemibold'>ヽ(ಠ益ಠ)ノ</Text>
    <Text type='BodySmallSemibold'>Digging ancient messages...</Text>
  </div>
)

export default MessageLoadingMore
