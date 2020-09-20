import React, {Component} from 'react';
import { Icon } from 'antd';
import logo from '../assets/images/logo.svg';

export default class TopBar extends Component {
    render() {
        return (
            <header className="App-header">
               <img src={logo} alt="logo" className="App-logo"/>
               <span className="App-title">Nearby</span>

               {this.props.isLoggedIn ?
                   <a className="logout" onClick={this.props.handleLogout} >
                       <Icon type="logout"/>{' '}Logout
                   </a> : null }
           </header>
        )
    }
}
